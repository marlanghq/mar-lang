// Sound (docs/proposals/sound.md): chip-audio synthesis for iOS, the native
// mirror of the WebAudio synth in internal/jsserve/runtime.js. The .mar builders
// (Sound.tone / volume / sweep / hold / duty / vibrato / arp / lowCut / highCut /
// chord / sequence / rest) assemble an opaque Sound VALUE: a `__Snd` ctor carrying
// a list of voice records with the SAME field set as the JS `{ wave, freq, ms,
// endFreq, holdMs, volume, delayMs, duty, vibDepth, vibRate, arp, lowCut,
// highCut }`. Sound.play is a Cmd; loop /
// ambient / once are Subs the page runtime starts/stops. The synth itself is an
// AVAudioEngine source node that sums the active voices sample by sample.
//
// Behavior parity (envelope shape, exact timbre) is tuned on device: this file
// is compile-checked, not run, in the build environment.

import Foundation
import AVFoundation

// MARK: - Voice model

/// One synthesised voice. Mirrors the JS voice record field-for-field.
private struct Voice {
    var wave: String        // "Square" | "Triangle" | "Sawtooth" | "Noise" | "Rest"
    var freq: Double        // Hz
    var ms: Double          // duration
    var endFreq: Double     // sweep target (0 = none)
    var holdMs: Double      // hold f0 this long before gliding to endFreq
    var volume: Double      // 0..100
    var delayMs: Double     // start offset within the Sound
    var duty: Double        // Square pulse width 1..99 (0/50 = plain square)
    var vibDepth: Double    // vibrato depth in cents (0 = none)
    var vibRate: Double     // vibrato rate in Hz
    var arp: [Double]       // extra pitches to step through (empty = none)
    var lowCut: Double      // Sound.lowCut  - trim below this Hz (0 = none)
    var highCut: Double     // Sound.highCut: trim above this Hz (0 = none)
    // The envelope, in ms (Sound.attack / Sound.release). Carried by the voice so
    // every playback path shapes it the same way; 0 means "unasked", which the
    // renderer floors at the shortest ramp that is not a click.
    var attack: Double
    var release: Double
    // Sound.decay: the stage between attack and the hold. `decay` is the fall
    // time in ms and `sustain` the level it falls TO, as a percent of this
    // voice's own volume. decay 0 means the voice never asked, and then the
    // envelope is exactly the attack / hold / release it always was.
    var decay: Double
    var sustain: Double
    // Sound.detune: a fixed offset in cents, applied as a frequency ratio right
    // where vibrato applies its own. The web reaches the same number through the
    // oscillator's `detune` AudioParam.
    var detune: Double
    // Sound.pan: stereo position, -100 hard left through 0 centre to +100 hard
    // right. Carried on the voice rather than applied by the playback path, so a
    // Sound placed once speaks from the same spot however it is played.
    var pan: Double
}

/// The pan law, shared by every path that mixes a voice into the output.
///
///     gainL = 1 - max(0,  pan) / 100
///     gainR = 1 - max(0, -pan) / 100
///
/// Centre is 1.0/1.0, so an unpanned voice is bit-identical to the mono output
/// this engine produced before it went stereo, and the 3 dB is paid only by
/// whoever asks to be placed hard to one side. That is deliberately NOT the
/// equal-power law (0.707 per channel at centre) that the web would get for free
/// from a StereoPannerNode fed a mono signal: equal power would have quietened
/// every existing sound in every app the day pan shipped. Both runtimes compute
/// this same arithmetic by hand; runtime.js says so in soundPanOut.
@inline(__always) private func panGains(_ pan: Double) -> (Double, Double) {
    let p = max(-100, min(100, pan))
    return (1 - max(0, p) / 100, 1 - max(0, -p) / 100)
}

/// The floor below which a gain change is a step discontinuity: a click. Not a
/// house style: the only number the engine imposes, and it is physics. Every
/// shape past it is taste and lives in the app (ADR-0006).
private let soundMinRampMs: Double = 5
private func rampSec(_ ms: Double) -> Double { max(soundMinRampMs, ms) / 1000 }

/// PolyBLEP: what keeps a hard-edged wave from aliasing.
///
/// A square or a sawtooth computed straight from the phase (`2p - 1`) has a
/// vertical edge, and a vertical edge has harmonics forever. Everything above
/// half the sample rate folds back down as inharmonic content, heard not as
/// brightness but as a metallic, tense edge that worsens with pitch. At 900Hz,
/// a pitch Myrkheim actually uses, most of the harmonic series is past Nyquist
/// and comes back as hash between the real partials.
///
/// The web side never had this: it asks the browser for an `OscillatorNode`,
/// which the Web Audio spec requires to be band-limited, and builds
/// `Sound.duty` from a 32-harmonic PeriodicWave. This synth computes its own
/// samples, so it does its own band-limiting, and this is the cheap standard
/// way: near an edge, replace the sample with a polynomial that approximates
/// the same step without the infinite series.
///
/// `t` is the phase 0..1 and `dt` the phase advanced per sample, so `dt` is the
/// width of an edge in phase. Away from an edge the correction is exactly zero,
/// which is why a triangle and a rest pay nothing for this.
@inline(__always) private func polyBlep(_ t: Double, _ dt: Double) -> Double {
    if dt <= 0 || dt >= 0.5 { return 0 }     // nothing to smooth at DC or past Nyquist
    if t < dt {
        let x = t / dt
        return x + x - x * x - 1
    }
    if t > 1 - dt {
        let x = (t - 1) / dt
        return x * x + x + x + 1
    }
    return 0
}

/// A voice placed on the engine's sample timeline, with per-voice synthesis
/// state advanced by the render callback.
private final class LiveVoice {
    let v: Voice
    let startSample: Double
    let durSamples: Double
    var phase: Double = 0          // oscillator phase, 0..1
    var vibPhase: Double = 0       // LFO phase, 0..1
    var noiseHold = Double.random(in: -1...1)   // sample-and-hold value for Noise
    var lpState: Double = 0        // one-pole low-pass memory  (Sound.highCut)
    var hpState: Double = 0        // one-pole high-pass memory (Sound.lowCut)
    let group: Int                 // for once/loop cancellation
    // Stereo placement, resolved once here rather than per sample in the render
    // callback: pan cannot change over a note's life, and that loop runs 44100
    // times a second per voice.
    let gainL: Double
    let gainR: Double
    init(_ v: Voice, startSample: Double, durSamples: Double, group: Int) {
        self.v = v
        self.startSample = startSample
        self.durSamples = durSamples
        self.group = group
        (self.gainL, self.gainR) = panGains(v.pan)
    }
}

/// One voice of a held source (Sound.voice / Sound.glide): a node held
/// indefinitely: no duration,
/// no envelope re-trigger, whose pitch and level are SMOOTHED parameters
/// gliding toward retunable targets. This is the native analog of the web
/// bed (soundBedStart/soundBedSet in runtime.js): the sub keeps returning
/// the same bed with a new freq/volume each frame (an engine note tracking
/// speed), and the live node slides to it instead of restarting. The
/// per-sample smoothing constant is the setTargetAtTime time-constant
/// equivalent: cur += (target - cur) * k, k = 1 - exp(-1/(tau*sr)).
private final class BedVoice {
    let wave: String
    let duty: Double
    var phase: Double = 0
    var freq: Double               // current (smoothed)
    var freqTarget: Double
    var amp: Double = 0.0001       // current gain (smoothed)
    var ampTarget: Double
    var ampK: Double               // varies: fade-in / swell / fade-out
    var fadingOut = false
    var dead = false               // set by the render thread once faded out
    var lowCut: Double = 0         // Sound.lowCut  - trim below this Hz
    var highCut: Double = 0        // Sound.highCut: trim above this Hz
    var lpState: Double = 0
    var hpState: Double = 0
    // Sound.vibrato on a HELD voice: the LFO has no stop time, it breathes for as
    // long as the bed lives. Applied to the instantaneous frequency (not to
    // freqTarget) so the live retune above glides underneath it undisturbed.
    var vibDepth: Double = 0       // cents (0 = none)
    var vibRate: Double = 0        // Hz
    var vibPhase: Double = 0       // LFO phase, 0..1
    // Sound.detune on a HELD voice: a fixed cents offset. Like vibrato it rides
    // the instantaneous pitch and leaves freqTarget alone, so a live retune keeps
    // gliding underneath the offset instead of erasing it.
    var detune: Double = 0         // cents (0 = none)
    // Fade-out rate, from the voice's Sound.release. Held here because the sub
    // teardown has the node but not the Sound that built it.
    var stopK: Double = 0
    // Sound.pan on a HELD voice. Set from the Sound at start; a Sound.glide can
    // retune pitch and level but not position, which is why pan is part of the
    // held source's identity (see heldKey) rather than something to slide.
    var gainL: Double = 1
    var gainR: Double = 1
    init(wave: String, duty: Double, freq: Double, peak: Double, fadeInK: Double) {
        self.wave = wave
        self.duty = duty
        self.freq = freq
        self.freqTarget = freq
        self.ampTarget = peak
        self.ampK = fadeInK
    }
}

// MARK: - Engine

final class MarSound: @unchecked Sendable {
    static let shared = MarSound()

    private let sr: Double = 44_100
    private let engine = AVAudioEngine()
    private var source: AVAudioSourceNode?
    private var started = false

    // Shared with the realtime render thread: guarded by `lock`.
    private let lock = NSLock()
    private var voices: [LiveVoice] = []
    private var beds: [BedVoice] = []        // Sound.voice / Sound.glide held voices
    private var clock: Double = 0            // frames since engine start
    private var nextGroup = 1

    // Per-sample smoothing for the RETUNE of a live held source (setTargetAtTime
    // analogs, mirroring soundGlideTo in the web runtime): pitch glides in ~80ms,
    // and a NEW LEVEL lands at one of two speeds.
    //
    // One number, two meanings, and the split is Sound.voice vs Sound.glide
    // (ADR-0024). A bed's level is supposed to lag: a crowd that tracks each event
    // is heard as a rhythm, not as a background. A VOICE's level is not: a
    // polyphonic instrument scales its voices by how many are sounding, and that
    // has to land while the chord is still held. At 1.1s it took about three
    // seconds to arrive, so the notes already down kept their old level and the
    // chord went over full scale anyway.
    //
    // The fade-in / fade-out constants that used to live here are gone: those were
    // the envelope, and the envelope is the voice's now (Sound.attack /
    // Sound.release), computed per voice in startHeld.
    private static let bedFreqK      = 1 - exp(-1 / (0.08 * 44_100))
    private static let bedSwellK     = 1 - exp(-1 / (1.1 * 44_100))
    private static let voiceLevelK   = 1 - exp(-1 / (0.03 * 44_100))

    /// The soft ceiling on the master mix. The bus sums voices LINEARLY and
    /// nothing downstream was catching the result: an app that sounds several
    /// things at once could ask for more than full scale, and the output was hard
    /// clipped: heard as a chord that is not just louder but broken.
    ///
    /// Stateless on purpose, not a compressor: a compressor would duck the WHOLE
    /// mix whenever one sound got loud, a behaviour no app can see coming. This is
    /// a function of the sample and nothing else.
    ///
    /// Below the knee it is EXACTLY the identity, so anything already in range is
    /// untouched; above it the curve bends and asymptotes to 1, so full scale can
    /// be approached and never passed. Mirrors soundCeilingCurve in runtime.js.
    private static let ceilingKnee = 0.7
    @inline(__always) static func ceiling(_ x: Double) -> Double {
        let a = abs(x)
        if a <= ceilingKnee { return x }
        let k = ceilingKnee
        let y = k + (1 - k) * tanh((a - k) / (1 - k))
        return x < 0 ? -y : y
    }

    // App-owned audio controls (Sound.setMuted / Sound.master). masterLevel
    // mirrors the JS 0..0.5 headroom scaling; muted ducks everything sounding.
    //
    // The DEFAULT has to match soundMasterLevel in runtime.js, and for a while
    // it did not: 0.5 here against 0.35 there, so the same game came out 1.43x
    // louder on iOS (about 3 dB) than the web build it was tuned against. Worse
    // than the level itself, the extra gain crossed the 0.7 ceiling knee far
    // more often, and every crossing is harmonics: the sound was not just
    // louder, it was harsher. 0.35 is `Sound.master 70`, the headroom an app
    // gets before it asks for anything.
    private var muted = false
    private var masterLevel: Double = 0.35

    private init() {}

    // MARK: engine lifecycle

    /// Lazily start the engine + source node on first use. Safe to call
    /// repeatedly. Mirrors ensureAudio() in the JS runtime.
    private func ensure() {
        guard !started else { return }
        started = true
        #if os(iOS)
        let session = AVAudioSession.sharedInstance()
        try? session.setCategory(.ambient, mode: .default, options: [.mixWithOthers])
        try? session.setActive(true)
        #endif
        // Two channels, so Sound.pan has somewhere to put a voice. `standardFormat`
        // is non-interleaved float, so render() gets one buffer per channel.
        let fmt = AVAudioFormat(standardFormatWithSampleRate: sr, channels: 2)!
        let node = AVAudioSourceNode { [weak self] _, _, frameCount, audioBufferList -> OSStatus in
            guard let self else { return noErr }
            self.render(frameCount: Int(frameCount), abl: audioBufferList)
            return noErr
        }
        self.source = node
        engine.attach(node)
        engine.connect(node, to: engine.mainMixerNode, format: fmt)
        engine.mainMixerNode.outputVolume = 1
        do { try engine.start() } catch { started = false }
    }

    // MARK: realtime render

    private func render(frameCount: Int, abl: UnsafeMutablePointer<AudioBufferList>) {
        let ablp = UnsafeMutableAudioBufferListPointer(abl)
        let chL = ablp[0].mData!.assumingMemoryBound(to: Float.self)
        // Deinterleaved stereo gives one buffer per channel. If the graph ever
        // hands us a single buffer, fold the two sides down rather than writing
        // the left one twice: at centre (l == r) the fold is exactly the old
        // mono sample, and a hard pan comes out half as loud, which is the
        // honest mono answer.
        let chR = ablp.count > 1 ? ablp[1].mData!.assumingMemoryBound(to: Float.self) : nil

        lock.lock()
        let level = muted ? 0 : masterLevel
        let base = clock
        let live = voices
        let bs = beds
        lock.unlock()

        for frame in 0..<frameCount {
            let now = base + Double(frame)
            // One accumulator per ear. The synthesis itself is untouched: a voice
            // still produces ONE sample and pan only decides how much of it each
            // side gets. That is what keeps voiceSample and bedSample single
            // copies rather than a stereo pair that could drift apart.
            var l = 0.0
            var r = 0.0
            for lv in live {
                let t = now - lv.startSample
                if t < 0 || t >= lv.durSamples { continue }
                let x = MarSound.voiceSample(lv, t: t, sr: sr)
                l += x * lv.gainL
                r += x * lv.gainR
            }
            for b in bs {
                let x = MarSound.bedSample(b, sr: sr)
                l += x * b.gainL
                r += x * b.gainR
            }
            // The ceiling applies PER CHANNEL, which is what stops each side
            // clipping on its own. It does mean a loud hard pan nudges the
            // perceived position, since one side is past the knee while the other
            // is still linear. The web ceiling is a WaveShaper and behaves the
            // same way, so the two platforms agree about it.
            if let chR {
                chL[frame] = Float(MarSound.ceiling(l * level))
                chR[frame] = Float(MarSound.ceiling(r * level))
            } else {
                chL[frame] = Float(MarSound.ceiling((l + r) * 0.5 * level))
            }
        }

        lock.lock()
        clock = base + Double(frameCount)
        // Drop voices whose end is now in the past, and beds that
        // finished their fade-out.
        let cutoff = clock
        voices.removeAll { $0.startSample + $0.durSamples <= cutoff }
        beds.removeAll { $0.dead }
        lock.unlock()
    }

    /// One sample for a live voice at local time `t` (in frames). Advances the
    /// voice's phase accumulators. Mirrors soundVoice()'s envelope + sweep +
    /// hold + vibrato + duty + arp + noise behaviour from the JS synth.
    private static func voiceSample(_ lv: LiveVoice, t: Double, sr: Double) -> Double {
        let v = lv.v
        if v.wave == "Rest" { return 0 }
        let dur = lv.durSamples / sr
        let ts = t / sr                            // seconds since voice start
        let peak = max(0.0002, min(100, v.volume) / 100)

        // Amplitude envelope, from the VOICE (Sound.attack / Sound.release), both
        // clamped to the note's own span so a long release cannot eat the attack.
        let atk = max(0.0001, min(dur / 2, rampSec(v.attack)))
        let tail = min(dur - atk, rampSec(v.release))

        // EXPONENTIAL, not linear, on both ends. The web reaches this shape by
        // asking WebAudio for `exponentialRampToValueAtTime`, which moves by a
        // constant RATIO per unit time: the way an ear reads a fade, and the
        // shape every sound in this repo was tuned against. A linear ramp puts
        // far more energy in the first milliseconds, so the same note started
        // harder here than in the browser and ended more abruptly.
        //
        // The 0.0001 floor is the web's, and not a detail: an exponential ramp
        // can never reach zero, so both sides run to that value and let the
        // note end there. `peak` already carries the same 0.0002 floor the web
        // puts on its own `pk`.
        //
        // The held bed (bedSample) was never wrong: its `ampK` smoothing is an
        // exponential approach already. Only the one-shot voice was linear.
        let floorAmp = 0.0001
        // Sound.decay: the fall from the peak to a held level, in whatever space
        // the attack and the release leave behind. Exponential like the other two
        // stages, because a resonator loses energy in proportion to what it has.
        //
        // `sus` is FLOORED at floorAmp, and that is not cosmetic. The pow() form
        // is `from * pow(to/from, x)`; with a bare 0 target it computes
        // pow(0, 0) = 1 at the first sample -- FULL volume -- and exactly 0 for
        // every sample after, which is a step to silence, the opposite of a decay.
        // The web meets the same wall from the other side: an exponential ramp
        // cannot be handed a zero target at all.
        let dec = v.decay > 0 ? min(rampSec(v.decay), max(0, dur - atk - tail)) : 0
        let sus = dec > 0 ? max(floorAmp, peak * max(0, min(100, v.sustain)) / 100) : peak
        let holdStart = atk + dec
        let amp: Double
        if ts < atk {
            amp = floorAmp * pow(peak / floorAmp, ts / atk)
        } else if dec > 0 && ts < holdStart {
            amp = peak * pow(sus / peak, (ts - atk) / dec)
        } else if ts < max(holdStart, dur - tail) {
            amp = sus
        } else {
            let relStart = max(holdStart, dur - tail)
            let span = max(0.0001, dur - relStart)
            // The release leaves from the SUSTAIN level, not the peak: a note that
            // already decayed must not jump back up to fade from the top.
            amp = sus * pow(floorAmp / sus, min(1, (ts - relStart) / span))
        }

        // Instantaneous frequency: arp steps, else sweep with optional hold.
        var freq = max(1, v.freq)
        if !v.arp.isEmpty {
            let seq = [max(1, v.freq)] + v.arp.map { max(1, $0) }
            let step = Int(ts / 0.02)               // ~50 Hz step, one per frame-ish
            freq = seq[step % seq.count]
        } else if v.endFreq > 0 && v.endFreq != v.freq {
            let hold = min(dur, max(0, v.holdMs / 1000))
            if ts <= hold {
                freq = max(1, v.freq)
            } else {
                let g = (ts - hold) / max(0.0001, dur - hold)
                freq = max(1, v.freq) + (max(1, v.endFreq) - max(1, v.freq)) * min(1, g)
            }
        }
        // Sound.detune: a fixed cents offset as a frequency ratio, applied BEFORE
        // vibrato so the two compose the way they do on the web, where the static
        // value and the LFO sum on one `detune` AudioParam.
        if v.detune != 0 { freq *= pow(2, v.detune / 1200) }
        // Vibrato: sine LFO on detune (cents -> frequency ratio).
        if v.vibDepth > 0 {
            lv.vibPhase += v.vibRate / sr
            if lv.vibPhase >= 1 { lv.vibPhase -= 1 }
            let cents = v.vibDepth * sin(2 * Double.pi * lv.vibPhase)
            freq *= pow(2, cents / 1200)
        }

        // Oscillator.
        let dt = freq / sr                 // phase per sample: the width of an edge
        lv.phase += dt
        let wrapped = lv.phase >= 1        // one full cycle at the note's pitch
        if wrapped { lv.phase -= 1 }
        let p = lv.phase
        var osc: Double
        switch v.wave {
        case "Triangle":
            // Left naive on purpose: a triangle has no jump in VALUE, only in
            // slope, so its harmonics fall off as 1/n squared instead of 1/n.
            // The aliasing is there and an order of magnitude quieter, and the
            // fix for it (polyBLAMP) has scaling nobody here can check by ear.
            // The two waves with a vertical edge are the two corrected below.
            osc = 4 * abs(p - 0.5) - 1
        case "Sawtooth":
            osc = 2 * p - 1 - polyBlep(p, dt)   // one edge per cycle, at the wrap
        case "Sine":
            // Nothing to band-limit: a sine has exactly one partial, so there is
            // nothing above it to alias. It is the most EXPENSIVE wave here even
            // so -- one sin() per sample per voice, against the square's compare
            // plus two polyBLEP corrections.
            osc = sin(2 * Double.pi * p)
        case "Noise":
            // Sample-and-hold at the note's pitch: draw a new random value only
            // when the phase completes a cycle, and hold it in between. Low keys
            // hold each step for a long time (rumble), high keys churn through
            // them (hiss), so noise finally answers the key you pressed. It used
            // to redraw EVERY sample, which is full-band white noise: identical on
            // every key, and the reason NOI felt like a dead switch. The web side
            // reaches the same place from the other end, by resampling its noise
            // clip; both track the note, neither is sample-identical to the other
            // (true of this synth's noise all along).
            // freq 0 means "no pitch asked for", which is how every game in the
            // repo writes noise today (all 88 calls): back when noise ignored
            // the number entirely. Those keep the old full-band white noise, a
            // fresh value every sample. Without this the shared `max(1, freq)`
            // floor would hold ONE value for a whole second and turn every
            // explosion into silence. The web mirror is the `|| 440` fallback,
            // which lands the same place: rate 1.0, the clip at natural speed.
            if v.freq <= 0 {
                osc = Double.random(in: -1...1)
            } else {
                if wrapped { lv.noiseHold = Double.random(in: -1...1) }
                osc = lv.noiseHold
            }
        default: // Square, with optional duty
            let duty = (v.duty >= 1 && v.duty <= 99) ? v.duty / 100 : 0.5
            // Two edges per cycle: up at the wrap, down at the duty point. The
            // second correction reads the phase that is zero exactly when p
            // reaches `duty`, which puts it on the falling edge at any width.
            osc = (p < duty ? 1 : -1)
                + polyBlep(p, dt)
                - polyBlep(fmod(p + 1 - duty, 1), dt)
        }
        return shaped(lv, osc, sr: sr) * amp
    }

    /// Sound.lowCut / Sound.highCut, as one-pole filters.
    ///
    /// The web side has BiquadFilterNodes; here the synth runs sample by sample,
    /// so a one-pole is the honest equivalent: same direction and same corner,
    /// a gentler slope. Asking for neither returns the sample untouched, so the
    /// ordinary voice pays nothing.
    private static func shaped(_ lv: LiveVoice, _ x: Double, sr: Double) -> Double {
        var out = x
        if lv.v.highCut > 0 {
            let k = 1 - exp(-2 * Double.pi * min(lv.v.highCut, sr / 2) / sr)
            lv.lpState += (out - lv.lpState) * k
            out = lv.lpState
        }
        if lv.v.lowCut > 0 {
            let k = 1 - exp(-2 * Double.pi * min(lv.v.lowCut, sr / 2) / sr)
            lv.hpState += (out - lv.hpState) * k
            out = out - lv.hpState        // high-pass = signal minus its low-passed self
        }
        return out
    }

    /// One sample for a HELD bed voice. Slides freq + amp toward their
    /// targets (the setTargetAtTime analog), no envelope, no duration.
    /// Marks the voice dead once a fade-out lands so the render cleanup
    /// can drop it.
    private static func bedSample(_ b: BedVoice, sr: Double) -> Double {
        b.freq += (b.freqTarget - b.freq) * bedFreqK
        b.amp += (b.ampTarget - b.amp) * b.ampK
        if b.fadingOut && b.amp <= 0.00015 {
            b.dead = true
            return 0
        }
        if b.wave == "Noise" {
            // The web bed sums two decorrelated loops so the wash never repeats;
            // a single noise source is the native stand-in for that. The TONE,
            // though, is no longer guessed here: it comes from Sound.lowCut /
            // Sound.highCut on the value, like every other voice.
            return bedShaped(b, Double.random(in: -1...1) * 0.35, sr: sr) * b.amp
        }
        // Vibrato: sine LFO on detune (cents -> frequency ratio), same as
        // voiceSample. Modulates the instantaneous pitch, leaving the smoothed
        // b.freq (the retune target) alone.
        var f = b.freq
        if b.detune != 0 { f *= pow(2, b.detune / 1200) }
        if b.vibDepth > 0 {
            b.vibPhase += b.vibRate / sr
            if b.vibPhase >= 1 { b.vibPhase -= 1 }
            let cents = b.vibDepth * sin(2 * Double.pi * b.vibPhase)
            f *= pow(2, cents / 1200)
        }
        let dt = f / sr                    // as in voiceSample: the edge width
        b.phase += dt
        if b.phase >= 1 { b.phase -= 1 }
        let p = b.phase
        var osc: Double
        switch b.wave {
        case "Triangle":
            // Left naive on purpose: a triangle has no jump in VALUE, only in
            // slope, so its harmonics fall off as 1/n squared instead of 1/n.
            // The aliasing is there and an order of magnitude quieter, and the
            // fix for it (polyBLAMP) has scaling nobody here can check by ear.
            // The two waves with a vertical edge are the two corrected below.
            osc = 4 * abs(p - 0.5) - 1
        case "Sawtooth":
            osc = 2 * p - 1 - polyBlep(p, dt)   // one edge per cycle, at the wrap
        case "Sine":
            // Nothing to band-limit: a sine has exactly one partial, so there is
            // nothing above it to alias. It is the most EXPENSIVE wave here even
            // so -- one sin() per sample per voice, against the square's compare
            // plus two polyBLEP corrections.
            osc = sin(2 * Double.pi * p)
        default:
            let duty = (b.duty >= 1 && b.duty <= 99) ? b.duty / 100 : 0.5
            // Two edges per cycle: up at the wrap, down at the duty point. The
            // second correction reads the phase that is zero exactly when p
            // reaches `duty`, which puts it on the falling edge at any width.
            osc = (p < duty ? 1 : -1)
                + polyBlep(p, dt)
                - polyBlep(fmod(p + 1 - duty, 1), dt)
        }
        return bedShaped(b, osc, sr: sr) * b.amp
    }

    /// Same one-pole shaping as `shaped`, for held bed voices.
    private static func bedShaped(_ b: BedVoice, _ x: Double, sr: Double) -> Double {
        var out = x
        if b.highCut > 0 {
            let k = 1 - exp(-2 * Double.pi * min(b.highCut, sr / 2) / sr)
            b.lpState += (out - b.lpState) * k
            out = b.lpState
        }
        if b.lowCut > 0 {
            let k = 1 - exp(-2 * Double.pi * min(b.lowCut, sr / 2) / sr)
            b.hpState += (out - b.hpState) * k
            out = out - b.hpState
        }
        return out
    }

    // MARK: scheduling

    /// Schedule every voice of a Sound to start `atMs` from now, in `group`.
    /// Returns the group id so a caller (once) can stop it early.
    @discardableResult
    private func schedule(_ snd: MarValue, atOffsetMs: Double = 20) -> Int {
        ensure()
        let vs = MarSound.voicesOf(snd)
        if vs.isEmpty { return 0 }
        lock.lock()
        let group = nextGroup; nextGroup += 1
        let start0 = clock + atOffsetMs / 1000 * sr
        for v in vs {
            let start = start0 + v.delayMs / 1000 * sr
            let dur = max(0.02, v.ms / 1000) * sr
            voices.append(LiveVoice(v, startSample: start, durSamples: dur, group: group))
        }
        lock.unlock()
        return group
    }

    private func stopGroup(_ group: Int) {
        lock.lock()
        voices.removeAll { $0.group == group }
        lock.unlock()
    }

    /// The span (ms) of a Sound = max(delayMs + ms) across its voices: the
    /// natural loop period.
    private static func spanMs(_ snd: MarValue) -> Double {
        voicesOf(snd).reduce(0) { max($0, $1.delayMs + $1.ms) }
    }

    /// A stable content key for a Sound: the sub reconciler uses it so a
    /// changed loop/ambient/once swaps rather than restarting on every render
    /// (mirrors soundFullKey in the JS runtime).
    static func contentKey(_ snd: MarValue) -> String {
        voicesOf(snd).map {
            "\($0.wave)|\($0.freq)|\($0.ms)|\($0.endFreq)|\($0.holdMs)|\($0.volume)|\($0.delayMs)|\($0.duty)|\($0.vibDepth)|\($0.vibRate)|\($0.arp)|\($0.lowCut)|\($0.highCut)|\($0.attack)|\($0.release)|\($0.decay)|\($0.sustain)|\($0.detune)|\($0.pan)"
        }.joined(separator: ";")
    }

    /// The identity of a HELD source: one node kept alive by a Sub, as opposed
    /// to a note scheduled and forgotten (mirrors heldKey in the JS runtime).
    /// Whatever is left OUT becomes a live parameter: handing the sound back
    /// with only that part changed glides the running node (glideTo) instead of
    /// stopping and restarting it, which would click AND, at 60 renders/sec,
    /// stall the frame rate.
    ///
    /// Volume is always out, so a held sound can swell. `withFreq` is the ONLY
    /// difference between the two held Subs:
    ///
    ///   Sound.voice  true   pitch is identity -> two pitches are two voices,
    ///                       which is polyphony: one voice per held key.
    ///   Sound.glide  false  pitch is a param  -> one source that slides to
    ///                       whatever pitch it is handed. Monophonic by
    ///                       construction, like glide on a synth.
    ///
    /// A structural change (different wave / voice count / tone shaping) swaps
    /// the source either way: the filter is built once and never glides.
    static func heldKey(_ snd: MarValue, withFreq: Bool) -> String {
        voicesOf(snd).map {
            "\($0.wave)|\(withFreq ? $0.freq : 0)|\($0.ms)|\($0.endFreq)|\($0.holdMs)|\($0.delayMs)|\($0.duty)|\($0.vibDepth)|\($0.vibRate)|\($0.arp)|\($0.lowCut)|\($0.highCut)|\($0.attack)|\($0.release)|\($0.decay)|\($0.sustain)|\($0.detune)|\($0.pan)"
        }.joined(separator: ";")
    }

    // MARK: public API used by the builtins + page runtime

    func playOnce(_ snd: MarValue) { _ = schedule(snd) }

    func setMuted(_ b: Bool) { lock.lock(); muted = b; lock.unlock() }

    func setMaster(_ level0to100: Int) {
        lock.lock(); masterLevel = max(0, min(100, Double(level0to100))) / 100 * 0.5; lock.unlock()
    }

    /// A running loop / ambient / once, tracked so a Sub teardown can stop it.
    final class Handle {
        var timer: DispatchSourceTimer?
        var groups: [Int] = []
        fileprivate var bedRefs: [BedVoice] = []
    }

    /// The playhead of one looping Sound. `origin` is the frame at which the
    /// current pass through `due` began; `i` is how far into that pass the
    /// cursor has booked. A class, not a struct, because the timer closure
    /// mutates it across ticks.
    private final class LoopCursor {
        let due: [Voice]              // non-Rest, sorted by onset
        let periodSamples: Double
        let group: Int
        var origin: Double
        var i: Int = 0
        init(due: [Voice], periodSamples: Double, origin: Double, group: Int) {
            self.due = due
            self.periodSamples = periodSamples
            self.origin = origin
            self.group = group
        }
    }

    /// loop: replay the Sound seamlessly on its own period.
    ///
    /// IT BOOKS A SLICE, NOT A PERIOD, and on this side that is not a stutter
    /// but a throughput bug. `render` walks `voices` once PER SAMPLE; a voice
    /// scheduled for the future is skipped by a bounds check, but it is still
    /// visited. This used to append every voice of the whole sound up front and
    /// then append a whole fresh copy every period, so a three-minute score with
    /// ~3000 sounding voices left ~3000 entries in that array permanently: about
    /// 150 million iterations a second on the audio render thread, for work that
    /// is thrown away. Booking only what comes due inside the horizon keeps the
    /// array at the handful actually sounding.
    ///
    /// Mirrors soundLoopStart in runtime.js: same 0.14 s horizon, same 30 ms
    /// tick, same rule that anything already past is SKIPPED rather than booked.
    func startLoop(_ snd: MarValue) -> Handle {
        ensure()
        let h = Handle()
        // The period comes from ALL voices, rests included: a trailing
        // Sound.rest is how a piece pads its last bar.
        let periodSamples = max(0.05, MarSound.spanMs(snd) / 1000) * sr
        // Rests are then dropped from the CURSOR (they render to nothing and are
        // typically half the list), and the rest sorted by onset, which is what
        // makes a cursor possible at all.
        let due = MarSound.voicesOf(snd)
            .filter { $0.wave != "Rest" }
            .sorted { $0.delayMs < $1.delayMs }
        if due.isEmpty { return h }          // a loop of pure silence
        lock.lock()
        let group = nextGroup; nextGroup += 1
        // One group for the WHOLE loop, not one per period: stop() removes by
        // group, and the old shape grew h.groups without bound for as long as
        // the music played.
        let cursor = LoopCursor(due: due, periodSamples: periodSamples,
                                origin: clock + 0.02 * sr, group: group)
        lock.unlock()
        h.groups.append(group)
        pumpLoop(cursor)
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + 0.03, repeating: 0.03)
        timer.setEventHandler { [weak self] in self?.pumpLoop(cursor) }
        timer.resume()
        h.timer = timer
        return h
    }

    /// One look-ahead tick for a loop. Runs on the main queue and takes the same
    /// lock the render thread does, because it reads `clock` and appends to
    /// `voices`.
    private func pumpLoop(_ c: LoopCursor) {
        let horizon = 0.14 * sr
        lock.lock()
        defer { lock.unlock() }
        // While muted, keep the timer alive but book nothing, and hold the
        // playhead at the present so unmuting resumes from the top rather than
        // bursting to catch up.
        if muted {
            c.origin = clock + 0.02 * sr
            c.i = 0
            return
        }
        let now = clock
        // Anything already past is SKIPPED. A stalled timer (backgrounded app,
        // a long main-queue block) would otherwise dump the whole missed stretch
        // at once, which is the burst this scheduler exists to avoid.
        // Terminates because every iteration advances either the cursor or the
        // origin.
        while true {
            if c.i >= c.due.count { c.origin += c.periodSamples; c.i = 0 }
            if c.origin + c.due[c.i].delayMs / 1000 * sr >= now { break }
            c.i += 1
        }
        // Then book what is due inside the horizon. A period shorter than the
        // horizon simply wraps more than once here.
        while true {
            if c.i >= c.due.count { c.origin += c.periodSamples; c.i = 0 }
            let v = c.due[c.i]
            let start = c.origin + v.delayMs / 1000 * sr
            if start >= now + horizon { break }
            voices.append(LiveVoice(v, startSample: start,
                                    durSamples: max(0.02, v.ms / 1000) * sr, group: c.group))
            c.i += 1
        }
    }

    /// ambient: a steady bed, each voice becomes ONE held BedVoice (no
    /// duration, no envelope re-trigger; re-arming the sound on a timer
    /// audibly repeated the attack, which is what made the engine drone
    /// sound like a stuck loop). Fades in on start; glideTo glides
    /// it live; stop fades it out. Mirrors soundBedStart in runtime.js.
    func startHeld(_ snd: MarValue) -> Handle {
        ensure()
        let h = Handle()
        lock.lock()
        for v in MarSound.voicesOf(snd) where v.wave != "Rest" {
            // A held source settles at the voice's SUSTAIN level, not at its raw
            // peak -- that is what a sustain level means for something with no
            // end. The decay TIME cannot apply here: this path holds one node at
            // one level with no per-note clock to fall along, and building one
            // would rebuild the note renderer inside the bed, the drift
            // sound-envelope.md exists to prevent. Mirrors heldLevel() on the web.
            let full = max(0.0002, min(100, max(0, v.volume)) / 100)
            let peak = v.decay > 0
                ? max(0.0002, full * max(0, min(100, v.sustain)) / 100)
                : full
            // Fade in / out at the voice's own envelope, not at a house constant.
            // k = 1 - exp(-1/(tau*sr)) is the per-sample form of the web ramp.
            let b = BedVoice(wave: v.wave, duty: v.duty,
                             freq: max(1, v.freq), peak: peak,
                             fadeInK: 1 - exp(-1 / (rampSec(v.attack) * self.sr)))
            b.stopK = 1 - exp(-1 / (rampSec(v.release) * self.sr))
            b.lowCut = v.lowCut
            b.highCut = v.highCut
            b.vibDepth = v.vibDepth
            b.vibRate = v.vibRate
            b.detune = v.detune
            (b.gainL, b.gainR) = panGains(v.pan)
            beds.append(b)
            h.bedRefs.append(b)
        }
        lock.unlock()
        return h
    }

    /// Retune a LIVE bed to a new Sound without restarting it: pitch and
    /// volume glide to the new targets (soundBedSet parity: the racer's
    /// engine returns the same bed at a new freq every frame and the note
    /// slides, the "vrum" rising with speed). Small deltas are skipped,
    /// like the web, so a steady bed isn't re-targeted 60x a second.
    /// `promptLevel` is true for Sound.voice and false for Sound.glide: see
    /// voiceLevelK / bedSwellK above. It is the only thing that differs.
    func glideTo(_ h: Handle, _ snd: MarValue, promptLevel: Bool) {
        let vs = MarSound.voicesOf(snd).filter { $0.wave != "Rest" }
        lock.lock()
        for (b, v) in zip(h.bedRefs, vs) {
            let peak = max(0.0002, min(100, max(0, v.volume)) / 100)
            if abs(b.ampTarget - peak) >= 0.004 {
                b.ampTarget = peak
                b.ampK = promptLevel ? MarSound.voiceLevelK : MarSound.bedSwellK
            }
            let f = max(1, v.freq)
            if b.wave != "Noise" && abs(b.freqTarget - f) >= 1 {
                b.freqTarget = f
            }
        }
        lock.unlock()
    }

    /// once: play through a single time; teardown cancels a still-sounding tail.
    func startOnce(_ snd: MarValue) -> Handle {
        let h = Handle()
        h.groups.append(schedule(snd))
        return h
    }

    func stop(_ h: Handle) {
        h.timer?.cancel(); h.timer = nil
        for g in h.groups { stopGroup(g) }
        h.groups.removeAll()
        if !h.bedRefs.isEmpty {
            lock.lock()
            for b in h.bedRefs {
                b.fadingOut = true
                b.ampTarget = 0.0001
                b.ampK = b.stopK
            }
            lock.unlock()
            h.bedRefs.removeAll()
        }
    }

    // MARK: value <-> voice bridging

    /// Decode a `__Snd` Sound value into Swift voices.
    private static func voicesOf(_ snd: MarValue) -> [Voice] {
        guard case .ctor(let tag, let args, _) = snd, tag == "__Snd", let first = args.first,
              case .list(let recs) = first else { return [] }
        return recs.compactMap { voiceOf($0) }
    }

    private static func voiceOf(_ v: MarValue) -> Voice? {
        guard case .record(let f, _) = v else { return nil }
        func s(_ k: String) -> String { if case .string(let x)? = f[k] { return x }; return "" }
        func d(_ k: String) -> Double {
            switch f[k] { case .int(let x)?: return Double(x); case .float(let x)?: return x; default: return 0 }
        }
        var arp: [Double] = []
        if case .list(let xs)? = f["arp"] { arp = xs.compactMap { if case .int(let n) = $0 { return Double(n) }; return nil } }
        return Voice(wave: s("wave").isEmpty ? "Square" : s("wave"),
                     freq: d("freq"), ms: d("ms"), endFreq: d("endFreq"), holdMs: d("holdMs"),
                     volume: f["volume"] == nil ? 60 : d("volume"), delayMs: d("delayMs"),
                     duty: d("duty"), vibDepth: d("vibDepth"), vibRate: d("vibRate"), arp: arp,
                     lowCut: d("lowCut"), highCut: d("highCut"),
                     attack: d("attack"), release: d("release"),
                     decay: d("decay"),
                     sustain: f["sustain"] == nil ? 100 : d("sustain"),
                     detune: d("detune"),
                     pan: d("pan"))
    }

    // MARK: builtin registration

    static func register(_ env: Env) {
        // The Sound.Wave constructors (Square/Triangle/Sawtooth/Noise) come
        // from the generated registry: see MarBuiltinCtors.swift.

        // --- builders: assemble the opaque Sound value ---
        func mkVoice(wave: String, freq: Int, ms: Int) -> MarValue {
            .record(fields: [
                "wave": .string(wave), "freq": .int(freq), "ms": .int(ms),
                "endFreq": .int(0), "holdMs": .int(0), "volume": .int(60),
                "delayMs": .int(0), "duty": .int(0), "vibDepth": .int(0),
                "vibRate": .int(0), "arp": .unit,
                "lowCut": .int(0), "highCut": .int(0),
                "decay": .int(0), "sustain": .int(100), "detune": .int(0),
                "pan": .int(0),
            ], order: ["wave", "freq", "ms", "endFreq", "holdMs", "volume", "delayMs", "duty", "vibDepth", "vibRate", "arp", "lowCut", "highCut", "decay", "sustain", "detune", "pan"])
        }
        func mkSound(_ voices: [MarValue]) -> MarValue { .ctor(tag: "__Snd", args: [.list(voices)], origin: nil) }
        func voicesOfVal(_ snd: MarValue) -> [MarValue] {
            if case .ctor(let t, let a, _) = snd, t == "__Snd", let f = a.first, case .list(let xs) = f { return xs }
            return []
        }
        func setField(_ rec: MarValue, _ key: String, _ val: MarValue) -> MarValue {
            guard case .record(var f, let order) = rec else { return rec }
            f[key] = val
            return .record(fields: f, order: order)
        }
        // patchLast: clone voices, mutate the final one.
        func patchLast(_ snd: MarValue, _ f: (MarValue) -> MarValue) -> MarValue {
            var vs = voicesOfVal(snd)
            if let last = vs.last { vs[vs.count - 1] = f(last) }
            return mkSound(vs)
        }
        // patchAll: the envelope shapes EVERY voice, unlike volume/duty. A chord is
        // one note on several oscillators; if only the last layer took the attack
        // the others would still jump, so the note would click and speak twice.
        func patchAll(_ snd: MarValue, _ f: (MarValue) -> MarValue) -> MarValue {
            mkSound(voicesOfVal(snd).map(f))
        }
        func intArg(_ v: MarValue) -> Int { if case .int(let n) = v { return n }; return 0 }

        env.defineFn("soundTone", "Sound.tone", 3) { a in
            let wave: String = { if case .ctor(let t, _, _) = a[0] { return t }; return "Square" }()
            return mkSound([mkVoice(wave: wave, freq: intArg(a[1]), ms: intArg(a[2]))])
        }
        env.defineFn("soundVolume", "Sound.volume", 2) { a in patchLast(a[1]) { setField($0, "volume", .int(max(0, min(100, intArg(a[0]))))) } }
        env.defineFn("soundSweep", "Sound.sweep", 2) { a in patchLast(a[1]) { setField($0, "endFreq", .int(intArg(a[0]))) } }
        env.defineFn("soundLowCut", "Sound.lowCut", 2) { a in patchLast(a[1]) { setField($0, "lowCut", .int(max(0, intArg(a[0])))) } }
        env.defineFn("soundHighCut", "Sound.highCut", 2) { a in patchLast(a[1]) { setField($0, "highCut", .int(max(0, intArg(a[0])))) } }
        env.defineFn("soundHoldPitch", "Sound.holdPitch", 2) { a in patchLast(a[1]) { setField($0, "holdMs", .int(intArg(a[0]))) } }
        env.defineFn("soundAttack", "Sound.attack", 2) { a in patchAll(a[1]) { setField($0, "attack", .int(max(0, intArg(a[0])))) } }
        env.defineFn("soundRelease", "Sound.release", 2) { a in patchAll(a[1]) { setField($0, "release", .int(max(0, intArg(a[0])))) } }
        // decay : fall time in ms, then the level to hold, as a percent of this
        // voice's own volume. patchAll for the same reason attack and release are:
        // a chord is one note on several oscillators, so a per-layer envelope
        // would make it click and speak twice.
        env.defineFn("soundDecay", "Sound.decay", 3) { a in
            patchAll(a[2]) {
                setField(setField($0, "decay", .int(max(0, intArg(a[0])))),
                         "sustain", .int(max(0, min(100, intArg(a[1])))))
            }
        }
        // pan : -100 hard left, 0 centre, +100 hard right. patchAll, with the
        // envelope and NOT with detune below, whose signature is identical: a
        // counter-melody is a Sound.sequence, so patching the last voice would
        // place one note of thirty and leave the rest in the middle. Clamped here
        // and again in panGains, because the law reads pan directly and an
        // unclamped 400 would produce a NEGATIVE gain: a polarity inversion three
        // times too loud, which cancels against the other voices instead of
        // falling silent.
        env.defineFn("soundPan", "Sound.pan", 2) { a in patchAll(a[1]) { setField($0, "pan", .int(max(-100, min(100, intArg(a[0]))))) } }
        env.defineFn("soundDuty", "Sound.duty", 2) { a in patchLast(a[1]) { setField($0, "duty", .int(max(1, min(99, intArg(a[0]))))) } }
        // detune : signed cents on THIS voice. patchLast, unlike the envelope --
        // a unison is layers that DIFFER, so patching them all would defeat it.
        // Clamped to two octaves: past that it has stopped being a detune and
        // become a transpose, which Sound.tone's own pitch says better.
        env.defineFn("soundDetune", "Sound.detune", 2) { a in patchLast(a[1]) { setField($0, "detune", .int(max(-2400, min(2400, intArg(a[0]))))) } }
        env.defineFn("soundVibrato", "Sound.vibrato", 3) { a in patchLast(a[2]) { setField(setField($0, "vibDepth", .int(max(0, intArg(a[0])))), "vibRate", .int(max(1, intArg(a[1])))) } }
        env.defineFn("soundArp", "Sound.arp", 2) { a in
            let list: MarValue = { if case .list = a[0] { return a[0] }; return .list([]) }()
            return patchLast(a[1]) { setField($0, "arp", list) }
        }
        env.defineFn("soundRest", "Sound.rest", 1) { a in
            mkSound([setField(setField(mkVoice(wave: "Rest", freq: 0, ms: intArg(a[0])), "volume", .int(0)), "wave", .string("Rest"))])
        }
        env.defineFn("soundChord", "Sound.chord", 1) { a in
            guard case .list(let parts) = a[0] else { return mkSound([]) }
            return mkSound(parts.flatMap { voicesOfVal($0) })
        }
        env.defineFn("soundSequence", "Sound.sequence", 1) { a in
            guard case .list(let parts) = a[0] else { return mkSound([]) }
            var out: [MarValue] = []; var off = 0
            for part in parts {
                var span = 0
                for voice in voicesOfVal(part) {
                    let base = { if case .record(let f, _) = voice, case .int(let n)? = f["delayMs"] { return n }; return 0 }()
                    let ms = { if case .record(let f, _) = voice, case .int(let n)? = f["ms"] { return n }; return 0 }()
                    out.append(setField(voice, "delayMs", .int(base + off)))
                    span = max(span, base + ms)
                }
                off += span
            }
            return mkSound(out)
        }

        // --- play (Cmd) + loop/ambient/once (Sub) + app controls ---
        env.defineFn("soundPlay", "Sound.play", 1) { a in
            let snd = a[0]
            return .effect(MarEffect(tag: "soundPlay") { MarSound.shared.playOnce(snd); return .unit })
        }
        func soundSub(_ mode: String, _ snd: MarValue) -> MarValue {
            .ctor(tag: "__Sub", args: [.ctor(tag: "__SubSound", args: [.string(mode), snd], origin: nil)], origin: nil)
        }
        env.defineFn("soundLoop", "Sound.loop", 1) { a in soundSub("loop", a[0]) }
        env.defineFn("soundVoice", "Sound.voice", 1) { a in soundSub("voice", a[0]) }
        env.defineFn("soundGlide", "Sound.glide", 1) { a in soundSub("glide", a[0]) }
        env.defineFn("soundOnce", "Sound.once", 1) { a in soundSub("once", a[0]) }
        env.defineFn("soundSetMuted", "Sound.setMuted", 1) { a in
            let b: Bool = { if case .bool(let x) = a[0] { return x }; return false }()
            return .effect(MarEffect(tag: "soundSetMuted") { MarSound.shared.setMuted(b); return .unit })
        }
        env.defineFn("soundMaster", "Sound.master", 1) { a in
            let n = intArg(a[0])
            return .effect(MarEffect(tag: "soundMaster") { MarSound.shared.setMaster(n); return .unit })
        }

        // Note helpers: octave -> Hz (equal temperament, A4 = 440). Written as
        // literal defineFn calls (not a loop) so the drift test sees the names.
        func noteHz(_ semi: Int, _ oct: Int) -> Int {
            let midi = 12 * (oct + 1) + semi
            return Int((440 * pow(2, Double(midi - 69) / 12)).rounded())
        }
        env.defineFn("soundPitch_c",   "Sound.c",   1) { a in .int(noteHz(0, intArg(a[0]))) }
        env.defineFn("soundPitch_cs",  "Sound.cs",  1) { a in .int(noteHz(1, intArg(a[0]))) }
        env.defineFn("soundPitch_d",   "Sound.d",   1) { a in .int(noteHz(2, intArg(a[0]))) }
        env.defineFn("soundPitch_ds",  "Sound.ds",  1) { a in .int(noteHz(3, intArg(a[0]))) }
        env.defineFn("soundPitch_e",   "Sound.e",   1) { a in .int(noteHz(4, intArg(a[0]))) }
        env.defineFn("soundPitch_f",   "Sound.f",   1) { a in .int(noteHz(5, intArg(a[0]))) }
        env.defineFn("soundPitch_fs",  "Sound.fs",  1) { a in .int(noteHz(6, intArg(a[0]))) }
        env.defineFn("soundPitch_g",   "Sound.g",   1) { a in .int(noteHz(7, intArg(a[0]))) }
        env.defineFn("soundPitch_gs",  "Sound.gs",  1) { a in .int(noteHz(8, intArg(a[0]))) }
        env.defineFn("soundPitch_a",   "Sound.a",   1) { a in .int(noteHz(9, intArg(a[0]))) }
        env.defineFn("soundPitch_as_", "Sound.as_", 1) { a in .int(noteHz(10, intArg(a[0]))) }
        env.defineFn("soundPitch_b",   "Sound.b",   1) { a in .int(noteHz(11, intArg(a[0]))) }
    }
}
