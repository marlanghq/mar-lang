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
}

/// The floor below which a gain change is a step discontinuity: a click. Not a
/// house style: the only number the engine imposes, and it is physics. Every
/// shape past it is taste and lives in the app (ADR-0006).
private let soundMinRampMs: Double = 5
private func rampSec(_ ms: Double) -> Double { max(soundMinRampMs, ms) / 1000 }

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
    init(_ v: Voice, startSample: Double, durSamples: Double, group: Int) {
        self.v = v
        self.startSample = startSample
        self.durSamples = durSamples
        self.group = group
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
    // Fade-out rate, from the voice's Sound.release. Held here because the sub
    // teardown has the node but not the Sound that built it.
    var stopK: Double = 0
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
    private var muted = false
    private var masterLevel: Double = 0.5

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
        let fmt = AVAudioFormat(standardFormatWithSampleRate: sr, channels: 1)!
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
        let out = ablp[0].mData!.assumingMemoryBound(to: Float.self)

        lock.lock()
        let level = muted ? 0 : masterLevel
        let base = clock
        let live = voices
        let bs = beds
        lock.unlock()

        for frame in 0..<frameCount {
            let now = base + Double(frame)
            var sample = 0.0
            for lv in live {
                let t = now - lv.startSample
                if t < 0 || t >= lv.durSamples { continue }
                sample += MarSound.voiceSample(lv, t: t, sr: sr)
            }
            for b in bs {
                sample += MarSound.bedSample(b, sr: sr)
            }
            out[frame] = Float(MarSound.ceiling(sample * level))
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
        let atk = min(dur / 2, rampSec(v.attack))
        let tail = min(dur - atk, rampSec(v.release))
        let amp: Double
        if ts < atk {
            amp = peak * (ts / atk)
        } else if ts < max(atk, dur - tail) {
            amp = peak
        } else {
            let rel = (dur - ts) / max(0.0001, tail)
            amp = peak * max(0, rel)
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
        // Vibrato: sine LFO on detune (cents -> frequency ratio).
        if v.vibDepth > 0 {
            lv.vibPhase += v.vibRate / sr
            if lv.vibPhase >= 1 { lv.vibPhase -= 1 }
            let cents = v.vibDepth * sin(2 * Double.pi * lv.vibPhase)
            freq *= pow(2, cents / 1200)
        }

        // Oscillator.
        lv.phase += freq / sr
        let wrapped = lv.phase >= 1        // one full cycle at the note's pitch
        if wrapped { lv.phase -= 1 }
        let p = lv.phase
        var osc: Double
        switch v.wave {
        case "Triangle":
            osc = 4 * abs(p - 0.5) - 1
        case "Sawtooth":
            osc = 2 * p - 1
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
            osc = p < duty ? 1 : -1
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
        if b.vibDepth > 0 {
            b.vibPhase += b.vibRate / sr
            if b.vibPhase >= 1 { b.vibPhase -= 1 }
            let cents = b.vibDepth * sin(2 * Double.pi * b.vibPhase)
            f *= pow(2, cents / 1200)
        }
        b.phase += f / sr
        if b.phase >= 1 { b.phase -= 1 }
        let p = b.phase
        var osc: Double
        switch b.wave {
        case "Triangle":
            osc = 4 * abs(p - 0.5) - 1
        case "Sawtooth":
            osc = 2 * p - 1
        default:
            let duty = (b.duty >= 1 && b.duty <= 99) ? b.duty / 100 : 0.5
            osc = p < duty ? 1 : -1
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
            "\($0.wave)|\($0.freq)|\($0.ms)|\($0.endFreq)|\($0.holdMs)|\($0.volume)|\($0.delayMs)|\($0.duty)|\($0.vibDepth)|\($0.vibRate)|\($0.arp)|\($0.lowCut)|\($0.highCut)|\($0.attack)|\($0.release)"
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
            "\($0.wave)|\(withFreq ? $0.freq : 0)|\($0.ms)|\($0.endFreq)|\($0.holdMs)|\($0.delayMs)|\($0.duty)|\($0.vibDepth)|\($0.vibRate)|\($0.arp)|\($0.lowCut)|\($0.highCut)|\($0.attack)|\($0.release)"
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

    /// loop: replay the Sound seamlessly on its own period.
    func startLoop(_ snd: MarValue) -> Handle {
        let h = Handle()
        let period = max(0.05, MarSound.spanMs(snd) / 1000)
        h.groups.append(schedule(snd))
        let timer = DispatchSource.makeTimerSource(queue: .main)
        timer.schedule(deadline: .now() + period, repeating: period)
        timer.setEventHandler { [weak self] in h.groups.append(self?.schedule(snd, atOffsetMs: 0) ?? 0) }
        timer.resume()
        h.timer = timer
        return h
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
            let peak = max(0.0002, min(100, max(0, v.volume)) / 100)
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
                     attack: d("attack"), release: d("release"))
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
            ], order: ["wave", "freq", "ms", "endFreq", "holdMs", "volume", "delayMs", "duty", "vibDepth", "vibRate", "arp", "lowCut", "highCut"])
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
        env.defineFn("soundDuty", "Sound.duty", 2) { a in patchLast(a[1]) { setField($0, "duty", .int(max(1, min(99, intArg(a[0]))))) } }
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
