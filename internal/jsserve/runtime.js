// mar JS runtime. Interprets a mar AST in the browser and runs an MVU loop.
//
// Loaded into a page along with the program AST and the entry point. The
// runtime evaluates the program until it produces a special "mountApp"
// effect, then takes over and runs the init/update/view loop with real DOM.
//
// Dev tooling (time-travel panel, dev dock, SSE reload, error overlay) is
// gated behind the build-time constant `__MAR_DEV__`. The dev path leaves
// it as `true`; `mar build` runs the source through esbuild with
// `Define: __MAR_DEV__ = false`, and dead-code elimination drops every
// `if (__MAR_DEV__) { ... }` block from the production bundle.

(function (global) {
  'use strict';

  // Build-time dev flag. The dev server (internal/jsserve/server.go)
  // ships this file verbatim, leaving the value `true`. `mar build`
  // rewrites this exact line to `false` before passing the source to
  // esbuild, which then constant-folds + DCEs every `if (__MAR_DEV__)`
  // block out of the production bundle. `const` (not `var` / `let`)
  // so esbuild can prove the binding is immutable.
  const __MAR_DEV__ = true;

  // ---------- Value constructors ----------

  const VInt    = (n)        => ({ k: 'I', n });
  // VDuration: time interval, a whole number of milliseconds inside the
  // range of Int. Constructed only via Time.millis / .seconds / .minutes /
  // .hours / .days / .weeks and Time.diff, so unit confusion is impossible
  // at the call site.
  const VDuration = (ms) => ({ k: 'D', ms });

  // A Time is a millisecond from 0000-01-01T00:00:00.000Z to
  // 9999-12-31T23:59:59.999Z in the proleptic Gregorian calendar, UTC, as on
  // the server (internal/runtime/timerange.go) and the phone (MarCivil.swift),
  // and every Time is built in range: through timeOf, or right after a
  // check with inTimeRange where an out-of-range answer is not an error (a
  // Nothing). The calendar arithmetic is done here rather than with
  // JavaScript's Date, whose Date.UTC reads the years 0-99 as 1900-1999 and
  // whose Date.parse accepts far more than RFC 3339.
  const MIN_TIME_MS = -62167219200000;
  const MAX_TIME_MS = 253402300799999;
  const MS_PER_DAY = 86400000;
  const MIN_TIME_DAY = Math.floor(MIN_TIME_MS / MS_PER_DAY);
  const MAX_TIME_DAY = Math.floor(MAX_TIME_MS / MS_PER_DAY);

  function timeOutOfRange(source) {
    return new Error('Time out of range: ' + source + ' is outside the range of a Time' +
      ' (0000-01-01T00:00:00.000Z to 9999-12-31T23:59:59.999Z)');
  }

  // NaN is outside the range too.
  function inTimeRange(ms) { return ms >= MIN_TIME_MS && ms <= MAX_TIME_MS; }

  function timeOf(source, ms) {
    if (!inTimeRange(ms)) throw timeOutOfRange(source);
    return VTime(ms);
  }

  function isLeapYear(y) { return y % 4 === 0 && (y % 100 !== 0 || y % 400 === 0); }

  function daysInMonth(y, m) {
    if (m === 2) return isLeapYear(y) ? 29 : 28;
    return (m === 4 || m === 6 || m === 9 || m === 11) ? 30 : 31;
  }

  // daysFromCivilBig is the number of days from 1970-01-01 to y-m-1, plus
  // d-1, in BigInt so it is exact for every Mar Int (Howard Hinnant's
  // days_from_civil, as Go's daysFromCivil). m is 1n..12n.
  function daysFromCivilBig(y, m, d) {
    if (m <= 2n) y -= 1n;
    const era = (y >= 0n ? y : y - 399n) / 400n;
    const yoe = y - era * 400n;
    const doy = (153n * ((m + 9n) % 12n) + 2n) / 5n;
    const doe = yoe * 365n + yoe / 4n - yoe / 100n + doy;
    return era * 146097n + doe - 719468n + (d - 1n);
  }

  function daysFromCivil(y, m, d) {
    return Number(daysFromCivilBig(BigInt(y), BigInt(m), BigInt(d)));
  }

  // civilFromDays is the date of a day count from 1970-01-01, for a day of a
  // Time (small enough that every step is exact).
  function civilFromDays(days) {
    const z = days + 719468;
    const era = Math.floor(z / 146097);
    const doe = z - era * 146097;
    const yoe = Math.floor((doe - Math.floor(doe / 1460) + Math.floor(doe / 36524) - Math.floor(doe / 146096)) / 365);
    const doy = doe - (365 * yoe + Math.floor(yoe / 4) - Math.floor(yoe / 100));
    const mp = Math.floor((5 * doy + 2) / 153);
    const d = doy - Math.floor((153 * mp + 2) / 5) + 1;
    const m = mp < 10 ? mp + 3 : mp - 9;
    const y = yoe + era * 400;
    return { y: m <= 2 ? y + 1 : y, m, d };
  }

  // timeFromYMD is midnight UTC of y-m-d, the month and the day carried over
  // (month 13 is January of the next year, day 0 the last day of the month
  // before), as Go's timeFromYMD.
  function timeFromYMD(y, m, d) {
    const source = 'Time.fromYMD ' + y + ' ' + m + ' ' + d;
    const M = BigInt(m) - 1n;
    const carry = M >= 0n ? M / 12n : (M - 11n) / 12n;
    const days = daysFromCivilBig(BigInt(y) + carry, M - carry * 12n + 1n, 1n) + (BigInt(d) - 1n);
    if (days < BigInt(MIN_TIME_DAY) || days > BigInt(MAX_TIME_DAY)) throw timeOutOfRange(source);
    return timeOf(source, Number(days) * MS_PER_DAY);
  }

  // timeAddDays moves a Time by n days of 86400000 ms, keeping the time of
  // day.
  function timeAddDays(t, n) {
    const source = 'Time.addDays ' + n;
    const day = Math.floor(t.millis / MS_PER_DAY);
    const days = day + n;
    if (!(days >= MIN_TIME_DAY && days <= MAX_TIME_DAY)) throw timeOutOfRange(source);
    return timeOf(source, days * MS_PER_DAY + (t.millis - day * MS_PER_DAY));
  }

  // timeAddMonths moves a Time by n calendar months, keeping the time of day
  // and landing on the last day of a shorter month.
  function timeAddMonths(t, n, source) {
    const day = Math.floor(t.millis / MS_PER_DAY);
    const c = civilFromDays(day);
    const total = c.y * 12 + (c.m - 1) + n;
    const ny = Math.floor(total / 12);
    const nm = total - ny * 12 + 1;
    if (!(ny >= 0 && ny <= 9999)) throw timeOutOfRange(source);
    const nd = Math.min(c.d, daysInMonth(ny, nm));
    return timeOf(source, daysFromCivil(ny, nm, nd) * MS_PER_DAY + (t.millis - day * MS_PER_DAY));
  }

  // A Date is a day of the calendar, with no time of day and no time zone:
  // the number of days from 1970-01-01, from 0000-01-01 to 9999-12-31 (the
  // days of a Time), as on the server (internal/runtime/date.go) and the
  // phone (MarCivil.swift). Every Date is built through dateOf.
  function dateOutOfRange(source) {
    return new Error('Date out of range: ' + source + ' is outside the range of a Date' +
      ' (0000-01-01 to 9999-12-31)');
  }

  // NaN is outside the range too.
  function dateOf(source, days) {
    if (!(days >= MIN_TIME_DAY && days <= MAX_TIME_DAY)) throw dateOutOfRange(source);
    return VDate(days);
  }

  // dateFromYMD is the day y-m-d, the month and the day carried over as
  // timeFromYMD carries them, in BigInt so a large argument is exact.
  function dateFromYMD(y, m, d) {
    const source = 'Date.fromYMD ' + y + ' ' + m + ' ' + d;
    const M = BigInt(m) - 1n;
    const carry = M >= 0n ? M / 12n : (M - 11n) / 12n;
    const days = daysFromCivilBig(BigInt(y) + carry, M - carry * 12n + 1n, 1n) + (BigInt(d) - 1n);
    if (days < BigInt(MIN_TIME_DAY) || days > BigInt(MAX_TIME_DAY)) throw dateOutOfRange(source);
    return dateOf(source, Number(days));
  }

  // dateAddMonths moves a Date by n calendar months, landing on the last day
  // of a shorter month.
  function dateAddMonths(dt, n, source) {
    const c = civilFromDays(dt.days);
    const total = c.y * 12 + (c.m - 1) + n;
    const ny = Math.floor(total / 12);
    const nm = total - ny * 12 + 1;
    if (!(ny >= 0 && ny <= 9999)) throw dateOutOfRange(source);
    return dateOf(source, daysFromCivil(ny, nm, Math.min(c.d, daysInMonth(ny, nm))));
  }

  // formatDateISO writes a Date as YYYY-MM-DD; every Date is in range, so the
  // year has four digits.
  function formatDateISO(dt) {
    const c = civilFromDays(dt.days);
    const p2 = (n) => String(n).padStart(2, '0');
    return String(c.y).padStart(4, '0') + '-' + p2(c.m) + '-' + p2(c.d);
  }

  // parseDateISO reads the one text form of a Date the three runtimes accept,
  // YYYY-MM-DD and nothing else (Go's parseDateISO is the same function): the
  // day count, or null.
  function parseDateISO(str) {
    const m = /^([0-9]{4})-([0-9]{2})-([0-9]{2})$/.exec(str);
    if (!m) return null;
    const y = +m[1], mo = +m[2], d = +m[3];
    if (mo < 1 || mo > 12 || d < 1 || d > daysInMonth(y, mo)) return null;
    return daysFromCivil(y, mo, d);
  }

  // parseTimeISO reads the one text form of a Time the three runtimes
  // accept, RFC 3339 with its optional parts and nothing else (Go's
  // parseTimeISO is the same function):
  //
  //   YYYY-MM-DD 'T' HH:MM:SS [ '.' DIGIT+ ] ( 'Z' | ( '+' | '-' ) HH:MM )
  //
  // It answers the moment the text names, which can be outside the range
  // (9999-12-31T23:59:59-01:00), or null when the text is not in that form.
  function parseTimeISO(s) {
    let i = 0;
    const num = (n) => {
      if (i + n > s.length) return null;
      let v = 0;
      for (let k = 0; k < n; k++) {
        const c = s.charCodeAt(i + k);
        if (c < 48 || c > 57) return null;
        v = v * 10 + (c - 48);
      }
      i += n;
      return v;
    };
    const lit = (ch) => {
      if (s[i] === ch) { i++; return true; }
      return false;
    };
    const y = num(4); if (y === null || !lit('-')) return null;
    const mo = num(2); if (mo === null || !lit('-')) return null;
    const d = num(2); if (d === null || !lit('T')) return null;
    const h = num(2); if (h === null || !lit(':')) return null;
    const mi = num(2); if (mi === null || !lit(':')) return null;
    const sec = num(2); if (sec === null) return null;
    let milli = 0;
    if (lit('.')) {
      let k = 0;
      while (i < s.length && s.charCodeAt(i) >= 48 && s.charCodeAt(i) <= 57) {
        if (k < 3) milli = milli * 10 + (s.charCodeAt(i) - 48);
        k++; i++;
      }
      if (k === 0) return null;
      for (; k < 3; k++) milli *= 10;
    }
    let offset = 0;
    if (lit('Z')) {
      // UTC
    } else if (s[i] === '+' || s[i] === '-') {
      const sign = s[i] === '-' ? -1 : 1;
      i++;
      const oh = num(2); if (oh === null || !lit(':')) return null;
      const om = num(2); if (om === null || oh > 23 || om > 59) return null;
      offset = sign * (oh * 3600 + om * 60);
    } else {
      return null;
    }
    if (i !== s.length || mo < 1 || mo > 12 || d < 1 || d > daysInMonth(y, mo) || h > 23 || mi > 59 || sec > 59) return null;
    return (daysFromCivil(y, mo, d) * 86400 + h * 3600 + mi * 60 + sec - offset) * 1000 + milli;
  }

  // durationSecondsText writes milliseconds as seconds, exactly, with no
  // trailing zeros: 90000 is "90", 1500 is "1.5", -1 is "-0.001". Go's
  // DurationSecondsText is the same function.
  function durationSecondsText(ms) {
    const sign = ms < 0 ? '-' : '';
    const abs = Math.abs(ms);
    const frac = abs % 1000;
    const whole = String((abs - frac) / 1000);
    if (frac === 0) return sign + whole;
    return sign + whole + '.' + String(frac).padStart(3, '0').replace(/0+$/, '');
  }
  // VTime: absolute moment, Unix milliseconds. Constructed via
  // Time.now (effect) or Time.fromIso. Wire format is ISO 8601.
  const VTime = (millis) => ({ k: 'TM', millis });
  // VDate: a day of the calendar, days from 1970-01-01. Built through
  // dateOf. Wire format is `{"__date": "2026-11-17"}`.
  const VDate = (days) => ({ k: 'DT', days });
  // VAngle: a rotation, carried as deci-degrees in 0..3599. Built only
  // via Math.degrees / .deciDegrees / .turns, so the unit is named at
  // construction and nowhere else (ADR 0029); `deci` is normative but not
  // observable, since no builtin hands it back. Wire format is
  // `{"__angle": 450}`.
  const VAngle = (deci) => ({ k: 'A', deci });
  const VString = (s)        => ({ k: 'S', s });
  const VBool   = (b)        => ({ k: 'B', b });
  const VUnit   = ()         => ({ k: 'U' });
  const VList   = (xs)       => ({ k: 'L', xs });
  const VTuple  = (xs)       => ({ k: 'T', xs });
  const VRecord = (fields, order) => ({ k: 'R', fields, order });
  const VCtor   = (tag, args)=> ({ k: 'C', tag, args: args || [] });

  // Reading a field a record does not have. The typechecker makes this
  // unreachable for records that came from this program's types, so it
  // only fires when a record arrives from OUTSIDE them: in practice the
  // model `mar dev` preserved across a hot reload, from before the Model
  // gained or lost a field.
  //
  // It is worth a real message because the alternative is terrible: the
  // missing field reads as `undefined`, travels on as if it were a Mar
  // value, and blows up much later wherever something finally
  // dereferences it. The reported error then points at the runtime's own
  // innards ("undefined is not an object (evaluating 'c.b')") rather than
  // at the field nobody had.
  //
  // Callers guard on `=== undefined` before calling, so the `in` check
  // never runs on the hot path, and a legitimate Mar value is never
  // `undefined`, since every one of them is a tagged object.
  // Do two values have the same top-level shape? Used to decide whether a
  // model preserved across a hot reload still fits the program that just
  // replaced the one it came from. Records compare by field set; anything
  // else compares by kind, which catches a Model that changed from a
  // record to a union but not one that changed between two unions.
  function sameShape(a, b) {
    if (!a || !b) return false;
    if (a.k !== b.k) return false;
    if (a.k !== 'R') return true;
    const ak = Object.keys(a.fields);
    if (ak.length !== Object.keys(b.fields).length) return false;
    for (const k of ak) if (!(k in b.fields)) return false;
    return true;
  }

  function missingField(rec, name) {
    if (name in rec.fields) return;   // the field exists and holds undefined: not ours
    const had = (rec.order || Object.keys(rec.fields)).join(', ');
    throw new Error(
      'record has no field `' + name + '`\n\n' +
      'this record has: ' + (had || '(no fields)') + '\n\n' +
      'reading a field that does not exist is a type error, so this record ' +
      'did not come from this program\'s types. Under `mar dev` that usually ' +
      'means the page is still holding the model it loaded with, from before ' +
      'the Model changed. Reload the page.');
  }
  // VChar: Unicode scalar value, distinct from a 1-char VString. Same
  // model as Go's rune / Swift's Unicode.Scalar. JSON wire format is
  // {"__char": "x"} (see jsToMar / marToJs below). `c` is the integer
  // scalar value.
  const VChar = (c) => ({ k: 'Ch', c });
  // VDec, exact base-10 number: BigInt coefficient (bounded to 34
  // significant digits) + scale. 1.50 is {coef: 150n, scale: 2};
  // equality is numeric, scale is display metadata. Mirrors the Go
  // runtime's VDecimal (internal/runtime/decimal.go). Wire format is
  // {"__dec": "1.50"}, a string, so no JSON parser ever routes the
  // digits through binary floating point.
  const VDec = (coef, scale) => ({ k: 'De', coef, scale });
  // VDivision: the inert exact quotient `/` produces. Opaque on
  // purpose: no codec, no arithmetic, not comparable; only the
  // Decimal.rounded / exact / withRemainder resolvers turn it into a
  // value, which is where the precision gets written.
  const VDivision = (num, den) => ({ k: 'Dv', num, den });

  // ---------- Decimal arithmetic (BigInt) ----------
  //
  // Semantics mirror internal/runtime/decimal.go exactly: the
  // conformance vectors in decimal_test.go must produce identical
  // strings on both runtimes.

  const MAX_DEC_DIGITS = 34;

  // The second bound (ADR 0039): at most 34 places after the point. It bounds
  // the scale, which MAX_DEC_DIGITS does not: 1e-50 is one digit and fifty
  // places. Same name and value as maxDecimalPlaces in
  // internal/runtime/decimal.go, which says why.
  const MAX_DECIMAL_PLACES = 34;

  function decPow10(n) { return 10n ** BigInt(n); }

  function errDecimalOverflow(op) {
    return new Error(op + ': Decimal overflow — the result exceeds ' + MAX_DEC_DIGITS + ' significant digits');
  }

  function errDecimalPlaces(op) {
    return new Error(op + ': Decimal overflow — the result needs more than ' + MAX_DECIMAL_PLACES + ' decimal places');
  }

  function errScaleArgument(op, scale) {
    return new Error(op + ': scale ' + scale + ' is more than the ' + MAX_DECIMAL_PLACES + ' decimal places a Decimal can hold');
  }

  // The whole rule of the two bounds, placesToDrop in decimal.go. digits is a
  // magnitude with no leading zeros, not zero; scale is how many of them sit
  // after the point. Answers how many zeros at the right end must go for the
  // value to fit both bounds, or throws when zeros are not enough: a digit
  // that changes the value never goes.
  function decPlacesToDrop(op, digits, scale) {
    const over = Math.max(scale - MAX_DECIMAL_PLACES, digits.length - MAX_DEC_DIGITS, 0);
    if (over === 0) return 0;
    // Counted by a loop, not by /0+$/: that pattern is not anchored at the
    // start, so on '1' + a long run of zeros + '1' it restarts at every zero
    // of the run: Decimal.fromString of 64K zeros took 2 s, 128K 7.7 s and
    // 256K 31 s.
    // The text is the sender's, and the count has to be linear in it.
    let end = digits.length;
    while (end > 0 && digits.charCodeAt(end - 1) === 48) end--;
    const zeros = Math.min(digits.length - end, scale);
    if (over > zeros) {
      throw scale - zeros > MAX_DECIMAL_PLACES ? errDecimalPlaces(op) : errDecimalOverflow(op);
    }
    return over;
  }

  const DEC_DIGITS_CEILING = 10n ** BigInt(MAX_DEC_DIGITS);

  // Every result is made here: coef at scale, held to both bounds. A value
  // that fits comes back exactly as given; zero always fits, at 34 places at
  // most. fitDecimal in decimal.go.
  function decFit(op, coef, scale) {
    if (coef === 0n) return VDec(0n, Math.min(scale, MAX_DECIMAL_PLACES));
    const mag = coef < 0n ? -coef : coef;
    if (scale <= MAX_DECIMAL_PLACES && mag < DEC_DIGITS_CEILING) return VDec(coef, scale);
    const digits = mag.toString();
    const drop = decPlacesToDrop(op, digits, scale);
    if (drop === 0) return VDec(coef, scale);
    const kept = BigInt(digits.slice(0, digits.length - drop));
    return VDec(coef < 0n ? -kept : kept, scale - drop);
  }

  // Bring two decimals to a common scale (exact rescale of the
  // smaller-scale coefficient). Returns [coefA, coefB, scale].
  function decAlign(a, b) {
    let ca = a.coef, cb = b.coef, scale = a.scale;
    if (a.scale < b.scale) { ca = ca * decPow10(b.scale - a.scale); scale = b.scale; }
    else if (b.scale < a.scale) { cb = cb * decPow10(a.scale - b.scale); }
    return [ca, cb, scale];
  }

  function decAddV(a, b) {
    const [ca, cb, scale] = decAlign(a, b);
    return decFit('+', ca + cb, scale);
  }

  function decSubV(a, b) {
    const [ca, cb, scale] = decAlign(a, b);
    return decFit('-', ca - cb, scale);
  }

  function decMulV(a, b) {
    return decFit('*', a.coef * b.coef, a.scale + b.scale);
  }

  function decCmpV(a, b) {
    const [ca, cb] = decAlign(a, b);
    return ca < cb ? -1 : ca > cb ? 1 : 0;
  }

  // Canonical scale-faithful rendering: "-12.50", "0.05", "7".
  function decStr(v) {
    const neg = v.coef < 0n;
    let digits = (neg ? -v.coef : v.coef).toString();
    let out;
    if (v.scale === 0) {
      out = digits;
    } else {
      if (digits.length <= v.scale) digits = '0'.repeat(v.scale - digits.length + 1) + digits;
      const cut = digits.length - v.scale;
      out = digits.slice(0, cut) + '.' + digits.slice(cut);
    }
    if (neg) out = '-' + out;
    return out;
  }

  // Parse the canonical form (optional sign, digits, optional point +
  // digits). Anything else, exponents included, returns null, and so does a
  // text that does not fit both bounds. The bounds are applied to the TEXT,
  // so what BigInt reads is at most 34 digits. parseDecimalString in
  // decimal.go.
  function parseDecString(s) {
    let t = String(s).trim();
    if (t === '') return null;
    let neg = false;
    if (t[0] === '-') { neg = true; t = t.slice(1); }
    else if (t[0] === '+') { t = t.slice(1); }
    let intPart = t, fracPart = '';
    const dot = t.indexOf('.');
    if (dot >= 0) { intPart = t.slice(0, dot); fracPart = t.slice(dot + 1); }
    if (intPart === '' && fracPart === '') return null;
    if (!/^[0-9]*$/.test(intPart) || !/^[0-9]*$/.test(fracPart)) return null;
    return decFromDigits(neg, (intPart + fracPart).replace(/^0+/, ''), fracPart.length);
  }

  // digits has no leading zeros ('' for zero); scale is how many sit after
  // the point. null when the value does not fit (decimalFromDigits in
  // decimal.go, which errors where this answers null).
  function decFromDigits(neg, digits, scale) {
    if (digits === '') return VDec(0n, Math.min(scale, MAX_DECIMAL_PLACES));
    let drop;
    try { drop = decPlacesToDrop('Decimal', digits, scale); } catch (_) { return null; }
    const coef = BigInt(digits.slice(0, digits.length - drop));
    return VDec(neg ? -coef : coef, scale - drop);
  }

  function decRoundingTag(v) {
    if (v && v.k === 'C' && ['HalfEven', 'HalfUp', 'Down', 'Up', 'Floor', 'Ceiling'].includes(v.tag)) {
      return v.tag;
    }
    throw new Error('expected a Decimal.Rounding value');
  }

  // num/den rounded to `scale` places under `mode`. den must be
  // nonzero (callers handle the total-division zero case). Works in
  // integers: value = numC * 10^(scale + denS - numS) / denC.
  function decRoundQuotient(num, den, scale, mode) {
    const shift = scale + den.scale - num.scale;
    let n = num.coef, d = den.coef;
    if (shift > 0) n = n * decPow10(shift);
    else if (shift < 0) d = d * decPow10(-shift);
    const negative = (n < 0n) !== (d < 0n);
    if (n < 0n) n = -n;
    if (d < 0n) d = -d;
    let q = n / d;
    const r = n % d;
    let roundUp = false;
    if (r !== 0n) {
      switch (mode) {
        case 'Down': break; // toward zero: never up
        case 'Up': roundUp = true; break;
        case 'Floor': roundUp = negative; break;
        case 'Ceiling': roundUp = !negative; break;
        case 'HalfUp':
        case 'HalfEven': {
          const twice = r * 2n;
          if (twice > d) roundUp = true;
          else if (twice === d) {
            roundUp = mode === 'HalfUp' ? true : (q % 2n === 1n); // banker's: to the even neighbour
          }
          break;
        }
      }
    }
    if (roundUp) q = q + 1n;
    if (negative && q !== 0n) q = -q;
    return decFit('Decimal.rounded', q, scale);
  }

  const DEC_ONE = { coef: 1n, scale: 0 };

  function decToScaleV(v, scale, mode) {
    if (scale < 0) throw new Error('Decimal.toScale: negative scale ' + scale);
    if (scale > MAX_DECIMAL_PLACES) throw errScaleArgument('Decimal.toScale', scale);
    if (scale >= v.scale) return decFit('Decimal.toScale', v.coef * decPow10(scale - v.scale), scale);
    return decRoundQuotient(v, DEC_ONE, scale, mode);
  }

  function decToIntV(v, mode) {
    const r = decRoundQuotient(v, DEC_ONE, 0, mode);
    const n = Number(r.coef);
    if (!Number.isSafeInteger(n)) throw new Error('Decimal: value does not fit an Int');
    return n;
  }

  // VDict / VSet: ordered, polymorphic comparable-keyed containers.
  // Internal representation parallels the Go runtime: VDict.pairs is a
  // sorted array of {key, value}; VSet.items is a sorted array. Sort
  // key is cmpValues; "comparable" means Int / Float / String at
  // runtime. Same wire markers as the Go side (`__dict`, `__set`).
  const VDict   = (pairs)    => ({ k: 'M', pairs: pairs || [] });
  const VSet    = (items)    => ({ k: 'Se', items: items || [] });
  const VFn     = (params, body, env, native, arity, applied) =>
    ({ k: 'Fn', params, body, env, native, arity, applied: applied || [] });
  const VView   = (tag, attrs, children, text, msg) =>
    ({ k: 'V', tag, attrs: attrs || [], children: children || [], text: text || '', msg: msg });
  const VEffect = (run, tag) => ({ k: 'E', run, tag: tag || '' });

  // ---------- Math (integer trigonometry) ----------
  //
  // Semantics mirror internal/runtime/math.go and MarMath.swift exactly:
  // the conformance vectors in math_conformance_test.go must produce
  // identical integers on all three. No Math.sin from the host anywhere:
  // three libms would be three answers, and a replayed rules engine, a
  // bot-verified level and time travel all need one.

  // BEGIN GENERATED SINE TABLE (go generate ./internal/mathgen), DO NOT EDIT
  // sin(θ) × 1000, half-even, at every deci-degree from 0.0° to 90.0°.
  // 901 entries: index i is i/10 degrees, sinQuarter[900] is exactly 1000.
  // The other three quadrants fold onto this one; atan2 searches the same
  // numbers, so it cannot disagree with sin about where an angle is.
  const SIN_QUARTER = [
    0, 2, 3, 5, 7, 9, 10, 12, 14, 16, 17, 19, 21, 23, 24, 26,
    28, 30, 31, 33, 35, 37, 38, 40, 42, 44, 45, 47, 49, 51, 52, 54,
    56, 58, 59, 61, 63, 65, 66, 68, 70, 71, 73, 75, 77, 78, 80, 82,
    84, 85, 87, 89, 91, 92, 94, 96, 98, 99, 101, 103, 105, 106, 108, 110,
    111, 113, 115, 117, 118, 120, 122, 124, 125, 127, 129, 131, 132, 134, 136, 137,
    139, 141, 143, 144, 146, 148, 150, 151, 153, 155, 156, 158, 160, 162, 163, 165,
    167, 168, 170, 172, 174, 175, 177, 179, 181, 182, 184, 186, 187, 189, 191, 193,
    194, 196, 198, 199, 201, 203, 204, 206, 208, 210, 211, 213, 215, 216, 218, 220,
    222, 223, 225, 227, 228, 230, 232, 233, 235, 237, 239, 240, 242, 244, 245, 247,
    249, 250, 252, 254, 255, 257, 259, 261, 262, 264, 266, 267, 269, 271, 272, 274,
    276, 277, 279, 281, 282, 284, 286, 287, 289, 291, 292, 294, 296, 297, 299, 301,
    302, 304, 306, 307, 309, 311, 312, 314, 316, 317, 319, 321, 322, 324, 326, 327,
    329, 331, 332, 334, 335, 337, 339, 340, 342, 344, 345, 347, 349, 350, 352, 353,
    355, 357, 358, 360, 362, 363, 365, 367, 368, 370, 371, 373, 375, 376, 378, 379,
    381, 383, 384, 386, 388, 389, 391, 392, 394, 396, 397, 399, 400, 402, 404, 405,
    407, 408, 410, 412, 413, 415, 416, 418, 419, 421, 423, 424, 426, 427, 429, 431,
    432, 434, 435, 437, 438, 440, 442, 443, 445, 446, 448, 449, 451, 452, 454, 456,
    457, 459, 460, 462, 463, 465, 466, 468, 469, 471, 473, 474, 476, 477, 479, 480,
    482, 483, 485, 486, 488, 489, 491, 492, 494, 495, 497, 498, 500, 502, 503, 505,
    506, 508, 509, 511, 512, 514, 515, 517, 518, 520, 521, 522, 524, 525, 527, 528,
    530, 531, 533, 534, 536, 537, 539, 540, 542, 543, 545, 546, 548, 549, 550, 552,
    553, 555, 556, 558, 559, 561, 562, 564, 565, 566, 568, 569, 571, 572, 574, 575,
    576, 578, 579, 581, 582, 584, 585, 586, 588, 589, 591, 592, 593, 595, 596, 598,
    599, 600, 602, 603, 605, 606, 607, 609, 610, 612, 613, 614, 616, 617, 618, 620,
    621, 623, 624, 625, 627, 628, 629, 631, 632, 633, 635, 636, 637, 639, 640, 641,
    643, 644, 645, 647, 648, 649, 651, 652, 653, 655, 656, 657, 659, 660, 661, 663,
    664, 665, 667, 668, 669, 670, 672, 673, 674, 676, 677, 678, 679, 681, 682, 683,
    685, 686, 687, 688, 690, 691, 692, 693, 695, 696, 697, 698, 700, 701, 702, 703,
    705, 706, 707, 708, 710, 711, 712, 713, 714, 716, 717, 718, 719, 721, 722, 723,
    724, 725, 727, 728, 729, 730, 731, 733, 734, 735, 736, 737, 738, 740, 741, 742,
    743, 744, 745, 747, 748, 749, 750, 751, 752, 754, 755, 756, 757, 758, 759, 760,
    762, 763, 764, 765, 766, 767, 768, 769, 771, 772, 773, 774, 775, 776, 777, 778,
    779, 780, 782, 783, 784, 785, 786, 787, 788, 789, 790, 791, 792, 793, 794, 795,
    797, 798, 799, 800, 801, 802, 803, 804, 805, 806, 807, 808, 809, 810, 811, 812,
    813, 814, 815, 816, 817, 818, 819, 820, 821, 822, 823, 824, 825, 826, 827, 828,
    829, 830, 831, 832, 833, 834, 835, 836, 837, 838, 839, 840, 841, 842, 842, 843,
    844, 845, 846, 847, 848, 849, 850, 851, 852, 853, 854, 854, 855, 856, 857, 858,
    859, 860, 861, 862, 863, 863, 864, 865, 866, 867, 868, 869, 869, 870, 871, 872,
    873, 874, 875, 875, 876, 877, 878, 879, 880, 880, 881, 882, 883, 884, 885, 885,
    886, 887, 888, 889, 889, 890, 891, 892, 893, 893, 894, 895, 896, 896, 897, 898,
    899, 900, 900, 901, 902, 903, 903, 904, 905, 906, 906, 907, 908, 909, 909, 910,
    911, 911, 912, 913, 914, 914, 915, 916, 916, 917, 918, 918, 919, 920, 921, 921,
    922, 923, 923, 924, 925, 925, 926, 927, 927, 928, 928, 929, 930, 930, 931, 932,
    932, 933, 934, 934, 935, 935, 936, 937, 937, 938, 938, 939, 940, 940, 941, 941,
    942, 943, 943, 944, 944, 945, 946, 946, 947, 947, 948, 948, 949, 949, 950, 951,
    951, 952, 952, 953, 953, 954, 954, 955, 955, 956, 956, 957, 957, 958, 958, 959,
    959, 960, 960, 961, 961, 962, 962, 963, 963, 964, 964, 965, 965, 965, 966, 966,
    967, 967, 968, 968, 969, 969, 969, 970, 970, 971, 971, 972, 972, 972, 973, 973,
    974, 974, 974, 975, 975, 976, 976, 976, 977, 977, 977, 978, 978, 979, 979, 979,
    980, 980, 980, 981, 981, 981, 982, 982, 982, 983, 983, 983, 984, 984, 984, 985,
    985, 985, 985, 986, 986, 986, 987, 987, 987, 987, 988, 988, 988, 988, 989, 989,
    989, 990, 990, 990, 990, 991, 991, 991, 991, 991, 992, 992, 992, 992, 993, 993,
    993, 993, 993, 994, 994, 994, 994, 994, 995, 995, 995, 995, 995, 995, 996, 996,
    996, 996, 996, 996, 996, 997, 997, 997, 997, 997, 997, 997, 998, 998, 998, 998,
    998, 998, 998, 998, 998, 999, 999, 999, 999, 999, 999, 999, 999, 999, 999, 999,
    999, 999, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000, 1000,
    1000, 1000, 1000, 1000, 1000,
  ];
  // END GENERATED SINE TABLE

  const DECI_FULL = 3600;
  const DECI_QUARTER = 900;

  // Positive modulo: every Angle constructor wraps through here, so any Int
  // is a valid argument and a negative angle means what it should.
  const posMod = (n, m) => ((n % m) + m) % m;

  // sin/cos fold the whole circle onto the one quarter-wave table. Quadrant
  // 1 and 3 read it backwards, 2 and 3 negate: no interpolation, because
  // the table already holds every representable input.
  function sinDeci(a) {
    const q = Math.floor(a / DECI_QUARTER);
    const r = a - q * DECI_QUARTER;
    const v = (q & 1) === 0 ? SIN_QUARTER[r] : SIN_QUARTER[DECI_QUARTER - r];
    return (q & 2) === 0 ? v : -v;
  }
  const cosDeci = (a) => sinDeci(posMod(a + DECI_QUARTER, DECI_FULL));

  // Integer Newton, floored. Converges from above and stops the moment it
  // would go back up, which is exactly floor(sqrt(n)) for every n >= 1.
  function isqrtInt(n) {
    if (n <= 0) return 0;
    let x = n;
    let y = Math.floor((x + 1) / 2);
    while (y < x) {
      x = y;
      y = Math.floor((x + Math.floor(n / x)) / 2);
    }
    return x;
  }

  // atan2 searches the SAME table sin reads, which is what makes the two
  // structurally unable to disagree about where an angle is. See math.go
  // for the full derivation; this is a line-for-line port.
  function atan2Deci(y, x) {
    if (x === 0 && y === 0) return 0;
    let ax = Math.abs(x);
    let ay = Math.abs(y);
    // Fold into the first octant so the search range is 0..450, then halve
    // both legs until a product with a table entry (< 2^10) stays inside
    // 53 bits. Halving is exact integer arithmetic, so all three runtimes
    // drop the same bits.
    const swapped = ay > ax;
    if (swapped) { const t = ax; ax = ay; ay = t; }
    while (ax >= 1099511627776) { // 2^40
      ax = Math.floor(ax / 2);
      ay = Math.floor(ay / 2);
    }
    // Largest k with tan(k) <= ay/ax, i.e. sin(k)*ax <= cos(k)*ay.
    let lo = 0;
    let hi = 450;
    while (lo < hi) {
      const mid = Math.floor((lo + hi + 1) / 2);
      if (SIN_QUARTER[mid] * ax <= SIN_QUARTER[DECI_QUARTER - mid] * ay) lo = mid;
      else hi = mid - 1;
    }
    let inner = lo;
    if (inner < 450) {
      // Round to the nearer of k and k+1 by comparing the two cross
      // products: each is |v| times the sine of the angular error, so the
      // smaller one is the closer angle. A tie keeps the lower angle.
      const d0 = SIN_QUARTER[DECI_QUARTER - inner] * ay - SIN_QUARTER[inner] * ax;
      const d1 = SIN_QUARTER[inner + 1] * ax - SIN_QUARTER[DECI_QUARTER - inner - 1] * ay;
      if (d1 < d0) inner += 1;
    }
    if (swapped) inner = DECI_QUARTER - inner;
    let a;
    if (x >= 0) a = y >= 0 ? inner : DECI_FULL - inner;
    else a = y >= 0 ? 1800 - inner : 1800 + inner;
    return posMod(a, DECI_FULL);
  }

  // ---------- JSON ⇄ MarValue ----------
  //
  // Lifted to the IIFE level (rather than inside makeBuiltinEnv)
  // so that mountPages's auth helpers: fetchAuthMe in particular
  //, can reach jsToMar by closure scope. Without this, the user
  // record from /_auth/whoami decodes via a ReferenceError catch and
  // collapses to Nothing, which silently redirects authenticated
  // users back to /sign-in instead of mounting the protected page.

  // A response body that is not JSON answers with MAR's sentence, the same one
  // JSON.decode gives and the same one on every runtime. A body that IS JSON
  // but does not become a Mar value keeps its own words: those two failures
  // are not the same failure.
  //
  // The host parser's message is neither. V8's wording is a V8 detail, and
  // Foundation's is LOCALIZED -- the same broken response read differently
  // depending on the language the phone was set to, which is how a user could
  // be shown "The data couldn't be read because it isn't in the correct
  // format." where the browser said "Unexpected token '<'".
  // A response whose shape is not what the call site's type promised. The
  // sentence is the one Service.call already gives; the flag is what keeps the
  // `decode failed:` prefix off it.
  function wireMismatch(where, detail) {
    const e = new Error(where + ' does not match what this app expects: ' + detail);
    e.marWire = true;
    return e;
  }

  // What a 401 from a Service.call decides, as a function of three facts.
  // Shared with the phone -- MarHTTP.decideAuthExpiry is the same three lines --
  // and held to the same table in internal/parityfixture, because both sides
  // were READ as agreeing, and reading is not checking.
  //
  // What stays on the platform: WHERE the remembered path is kept and how it is
  // filtered. This side's ?next= rides in a URL a person can type, so it runs
  // isSafeReturnPath over it; the phone keeps it in memory, reached only from
  // its own nav stack.
  function decideAuthExpiry(signInPath, alreadyRedirecting, currentPath) {
    if (!signInPath) return { handled: false, navigate: false, capture: null };
    if (alreadyRedirecting) return { handled: true, navigate: false, capture: null };
    return {
      handled: true,
      navigate: true,
      capture: currentPath === signInPath ? null : currentPath,
    };
  }
  if (typeof globalThis !== 'undefined') globalThis.__marDecideAuthExpiry = decideAuthExpiry;

  // Something a CHECKED program cannot do. The Go evaluator drew this line and
  // kept it -- of its twelve errors only two survived as real program errors --
  // and this side had no line at all: a bug in Mar and a mistake in the user's
  // code arrived in the same voice, so a user read a sentence that sounded like
  // their fault and the report never came.
  //
  // Keep the sentence identical to internalErrorf in internal/runtime and to
  // MarRuntimeError.internalText on the phone; the parity fixture compares it.
  //
  // `no case branch matched` deliberately stays out of this: a case expression
  // that matched nothing is the user's program, both clients match on its text,
  // and there is a test that says so.
  function marInternal(what) {
    return new Error('internal error: ' + what +
      ' — a checked program cannot do this, so this is a bug in Mar rather than in your code. Please report it.');
  }

  function jsonBodyOrThrow(body) {
    try {
      return JSON.parse(body);
    } catch (_) {
      throw new Error('the document is not valid JSON');
    }
  }

  // parseJSONExact parses a document WITHOUT letting the engine's number
  // parser see the numbers.
  //
  // JSON.parse hands back a float64, and by then the digits are gone. Measured
  // on one document, in the browser, against what the sender wrote:
  //
  //   1000000000000000000000000000000  ->  String(n) === "1e+30"
  //   12345678901234567890             ->  "12345678901234567000"
  //   9007199254740993                 ->  "9007199254740992"
  //
  // The first two are values Mar CAN hold exactly -- a Decimal is 34
  // significant digits -- and the engine had already lost them before any Mar
  // code ran. So the number literals are lifted out of the text first, as
  // {"__num": "<the literal>"}, and jsToMar reads the digits rather than a
  // double. The structure, the escapes and the syntax errors stay JSON.parse's
  // job, which is the part it is good at.
  //
  // The scan only has to know one thing to be correct: outside a string, JSON
  // has no digits except in numbers. `true`, `false` and `null` have none.
  function liftNumberLiterals(text) {
    let out = '';
    let i = 0;
    while (i < text.length) {
      const c = text[i];
      if (c === '"') {
        // Copy the string whole, escapes included, so a digit inside it is
        // never mistaken for a number.
        let j = i + 1;
        while (j < text.length) {
          if (text[j] === '\\') { j += 2; continue; }
          if (text[j] === '"') { j++; break; }
          j++;
        }
        out += text.slice(i, j);
        i = j;
        continue;
      }
      if (c === '-' || (c >= '0' && c <= '9')) {
        let j = i;
        if (text[j] === '-') j++;
        while (j < text.length && /[0-9.eE+\-]/.test(text[j])) j++;
        out += '{"__num":' + JSON.stringify(text.slice(i, j)) + '}';
        i = j;
        continue;
      }
      out += c;
      i++;
    }
    return out;
  }

  // rawNumberOf reads a JSON number that may have been lifted out of the text,
  // and returns undefined for anything that is not a number at all.
  function rawNumberOf(v) {
    if (typeof v === 'number') return v;
    if (v && typeof v === 'object' && typeof v.__num === 'string' && Object.keys(v).length === 1) {
      const n = Number(v.__num);
      return Number.isFinite(n) ? n : undefined;
    }
    return undefined;
  }

  // numberLiteralOf is a JSON number's own text: the lifted literal after
  // parseJSONExact, or the shortest text of a plain double from JSON.parse.
  // undefined for anything that is not a number.
  function numberLiteralOf(v) {
    if (typeof v === 'number') return String(v);
    if (v && typeof v === 'object' && typeof v.__num === 'string' && Object.keys(v).length === 1) return v.__num;
    return undefined;
  }

  function parseJSONExact(text) {
    return JSON.parse(liftNumberLiterals(text));
  }

  // numberFromLiteral turns a JSON number's own text into the Mar value it
  // names: an Int when it is whole and inside 53 bits, an exact Decimal when
  // it fits 34 significant digits and 34 places, and nothing otherwise.
  //
  // Nothing else is a number in Mar. There used to be a VFloat fallback; the
  // representation is gone now, because with this boundary closed nothing
  // could construct one. It had no type either -- TFloat does not exist, so a
  // checked program could never hold one -- and existed only to reach a shape
  // check and fail there, reporting "should be a number, got a number".
  //
  // decimalFromWireNumber reads exponent notation by MOVING THE POINT, never
  // by multiplying a double, which is the approximation this whole path exists
  // to avoid: 1e30 becomes a 1 and thirty zeros. The Decimal parser refuses
  // an exponent on purpose (what a Mar literal and `Decimal.fromString` accept
  // is the language's business); what a JSON document may carry is the
  // boundary's, and every serializer prints a large number in exponent form.
  //
  // The exponent is the sender's, so nothing is built from it before the
  // bounds are known to hold: reading 1e-999999999 and 1e999999999 alike used
  // to start with '0'.repeat(999999999), which Safari built (a gigabyte and
  // seconds) and Chrome refused with a RangeError instead of Mar's sentence.
  // Same steps and answers as decimalFromWireNumber in
  // internal/runtime/decimal.go; null where that one errors.
  function decimalFromWireNumber(lit) {
    const m = /^(-?)([0-9]*)(?:\.([0-9]*))?(?:[eE]([+-]?[0-9]+))?$/.exec(lit);
    if (!m) return null;
    const neg = m[1] === '-';
    const intPart = m[2] || '';
    const frac = m[3] || '';
    let exp = 0;
    if (m[4] !== undefined) {
      // An exponent outside 64 bits is one the server cannot read. More than
      // 19 digits is outside whatever they are, and is answered by length:
      // BigInt reads a long exponent in quadratic time.
      if (m[4].replace(/^[+-]?0*/, '').length > 19) return null;
      const bigExp = BigInt(m[4]);
      if (bigExp > 9223372036854775807n || bigExp < -9223372036854775808n) return null;
      // A double past 2^53 is not the exponent exactly, but every comparison
      // below is against a bound far inside that, so it answers the same.
      exp = Number(bigExp);
    }
    const digits = (intPart + frac).replace(/^0+/, '');
    // Below -(34 + the digits) the number keeps more than 34 places whatever
    // zeros its digits end with; a zero fits, at 34. In Go and Swift this is
    // what keeps the point arithmetic from wrapping or trapping; here exp is a
    // double and nothing wraps, so it is only the same early answer, kept so
    // the three read alike (decFromDigits would answer the same).
    if (exp < -(MAX_DECIMAL_PLACES + digits.length)) {
      return digits === '' ? VDec(0n, MAX_DECIMAL_PLACES) : null;
    }
    const scale = frac.length - exp;
    if (scale >= 0) return decFromDigits(neg, digits, scale);
    // The point moved past the last digit: zeros before the point count, and
    // the padding is refused before it is built. Zero needs none.
    if (digits === '') return VDec(0n, 0);
    if (-scale > MAX_DEC_DIGITS - digits.length) return null;
    const coef = BigInt(digits) * decPow10(-scale);
    return VDec(neg ? -coef : coef, 0);
  }

  // The JSON number grammar (RFC 8259): a lifted literal that does not match
  // it was never a number, and the document is not JSON.
  const JSON_NUMBER = /^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$/;

  function numberFromLiteral(lit) {
    if (!JSON_NUMBER.test(lit)) {
      throw new Error('the document is not valid JSON');
    }
    // A plain integer short enough to be exact stays an Int.
    if (/^-?[0-9]+$/.test(lit)) {
      const n = Number(lit);
      if (Number.isSafeInteger(n)) return VInt(n);
    }
    const d = decimalFromWireNumber(lit);
    if (d) return d;
    throw new Error('JSON: ' + lit + ' is not a number Mar can hold exactly:' +
      ' an Int is at most 9007199254740991 and a Decimal at most 34 significant digits and 34 decimal places');
  }

  // codePointCompare orders two strings by their Unicode scalars, which is
  // the byte order of their UTF-8 (Go's sort.Strings). The default sort
  // compares UTF-16 code units, which puts a character past U+FFFF before
  // one in U+E000..U+FFFF.
  function codePointCompare(a, b) {
    const n = Math.min(a.length, b.length);
    for (let i = 0; i < n; i++) {
      const x = a.codePointAt(i);
      const y = b.codePointAt(i);
      if (x !== y) return x < y ? -1 : 1;
      if (x > 0xFFFF) i++;
    }
    return a.length - b.length;
  }

  // wellFormed replaces each surrogate that is not half of a pair with
  // U+FFFD. A Mar String is a sequence of Unicode scalars on every runtime:
  // a JSON document can still spell a lone surrogate (`"\ud800"`), and the
  // server's decoder and the phone's (MarJSONCodec.parse) replace it the
  // same way.
  function wellFormed(s) {
    if (!/[\uD800-\uDFFF]/.test(s)) return s;
    let out = '';
    for (let i = 0; i < s.length; i++) {
      const c = s.charCodeAt(i);
      if (c >= 0xD800 && c <= 0xDBFF && i + 1 < s.length) {
        const d = s.charCodeAt(i + 1);
        if (d >= 0xDC00 && d <= 0xDFFF) {
          out += s[i] + s[i + 1];
          i++;
          continue;
        }
      }
      out += (c >= 0xD800 && c <= 0xDFFF) ? '\uFFFD' : s[i];
    }
    return out;
  }

  function jsToMar(v) {
    // null → VUnit. Previously null → VCtor('Nothing'), but that
    // collided with VUnit's wire encoding (Go encodes () as null):
    // a service `Int -> ()` returned null, decoded as Nothing on the
    // client, and pattern `Ok ()` failed at runtime. Both Maybe
    // constructors now tag uniformly via {"__ctor":"Nothing"} /
    // {"__ctor":"Just",...}, matching encodeValue on the Go side and
    // MarJSONCodec on iOS.
    if (v === null || v === undefined) return VUnit();
    if (typeof v === 'number') {
      // Reached only by a caller that parsed with JSON.parse rather than
      // parseJSONExact -- Mar's own wire format, whose numbers are already
      // bounded by what the server encoded. A foreign document goes through
      // numberFromLiteral and never arrives here as a double.
      if (Number.isInteger(v)) {
        if (!Number.isSafeInteger(v)) {
          throw new Error('Int out of range: ' + v +
            ' from JSON is outside the range of Int (-9007199254740991 to 9007199254740991)');
        }
        return VInt(v);
      }
      const d = parseDecString(String(v));
      if (d) return d;
      throw new Error('JSON: ' + v + ' is not a number Mar can hold exactly:' +
        ' an Int is at most 9007199254740991 and a Decimal at most 34 significant digits and 34 decimal places');
    }
    if (typeof v === 'string') return VString(wellFormed(v));
    if (typeof v === 'boolean') return VBool(v);
    if (Array.isArray(v)) return VList(v.map(jsToMar));
    if (typeof v === 'object') {
      // A number literal lifted out of the text by parseJSONExact, so the
      // digits the sender wrote are what gets read.
      if (typeof v.__num === 'string' && Object.keys(v).length === 1) {
        return numberFromLiteral(v.__num);
      }
      // Tagged constructor, round-trip from {__ctor: "Tag"} or
      // {__ctor: "Tag", __args: [...]}. Convention shared with the
      // Go encoder (encodeValue). Any object missing
      // __ctor falls through to the record path.
      if (typeof v.__ctor === 'string') {
        const args = Array.isArray(v.__args) ? v.__args.map(jsToMar) : [];
        return VCtor(wellFormed(v.__ctor), args);
      }
      // Time round-trip, `{__time: "ISO 8601"}` from the Go
      // encoder rebuilds a VTime so user code typed as
      // `createdAt : Time` actually receives a Time, not a String.
      // The text is read by parseTimeISO, the one form the three runtimes
      // accept, and the moment it names has to be in the range of a Time.
      if (typeof v.__time === 'string' && Object.keys(v).length === 1) {
        const iso = wellFormed(v.__time);
        const ms = parseTimeISO(iso);
        if (ms === null) {
          throw new Error('the value should be a date and time, got `' + iso + '`');
        }
        return timeOf('`' + iso + '`', ms);
      }
      // Date round-trip, `{__date: "2026-11-17"}`, read by parseDateISO, the
      // one form the three runtimes accept.
      if (typeof v.__date === 'string' && Object.keys(v).length === 1) {
        const text = wellFormed(v.__date);
        const days = parseDateISO(text);
        if (days === null) throw new Error('the value should be a date, got `' + text + '`');
        return dateOf('`' + text + '`', days);
      }
      // Angle round-trip, `{__angle: 450}` (deci-degrees) from the Go
      // encoder. Wrapped rather than trusted: the payload crossed a
      // network boundary and every other way to build an Angle
      // normalizes, so this one does too.
      // Deci-degrees are integral by construction and no Mar encoder
      // writes a fraction, so a fractional payload is refused rather than
      // truncated — the same rule `__char` follows, and the same sentence
      // in all three runtimes. Pinned by the Decode block of the corpus.
      // rawNumberOf, not `typeof === number`: after parseJSONExact every
      // number in the document is a lifted literal, so the tagged forms that
      // carry one have to read it the same way. `__angle` is the only tagged
      // form whose payload is a number -- __dec, __char and __time are
      // strings, __dict and __set are arrays -- and missing it turned every
      // Angle on the wire into "got an object with __angle".
      // Every marker is read only alone in its object, as on the server and
      // the phone; an object with more keys is a record.
      const alone = Object.keys(v).length === 1;
      const angleRaw = alone ? rawNumberOf(v.__angle) : undefined;
      if (angleRaw !== undefined) {
        if (!Number.isInteger(angleRaw)) {
          throw new Error('the value should be a whole number of deci-degrees, got ' + angleRaw);
        }
        return VAngle(posMod(angleRaw, DECI_FULL));
      }
      // Duration round-trip, `{__duration: 1500}`, whole milliseconds inside
      // the range of Int: an integer literal, read from its own text, by the
      // rules and sentences of Go's durationFromWire.
      if ('__duration' in v && Object.keys(v).length === 1) {
        const lit = numberLiteralOf(v.__duration);
        if (lit === undefined) throw new Error('the value should be a whole number of milliseconds');
        if (!JSON_NUMBER.test(lit)) throw new Error('the document is not valid JSON');
        if (!/^-?[0-9]+$/.test(lit)) {
          throw new Error('the value should be a whole number of milliseconds, got ' + lit);
        }
        const ms = Number(lit);
        if (!Number.isSafeInteger(ms)) {
          throw durationOutOfRange('a duration of ' + lit + ' milliseconds');
        }
        return VDuration(ms);
      }
      // Char round-trip, `{__char: "x"}` from the Go encoder, and
      // exactly one Unicode scalar or nothing.
      //
      // This used to take the FIRST scalar and degrade an empty
      // payload to U+FFFD. The input is EXTERNAL — a Mar encoder cannot
      // write more than one scalar — so `{"__char":"abc"}` answered 'a'
      // silently, and an empty payload answered U+FFFD here and on the
      // phone while the server errored. Three runtimes, three answers to
      // one JSON document. Pinned by the Decode block of the corpus.
      if (alone && typeof v.__char === 'string') {
        const scalars = Array.from(wellFormed(v.__char));
        if (scalars.length !== 1) {
          throw new Error('the value should be a single character, got ' + scalars.length);
        }
        return VChar(scalars[0].codePointAt(0));
      }
      // Decimal round-trip, `{__dec: "1.50"}` from the Go encoder.
      // Rebuilt textually so the exact coefficient + scale survive.
      // A tagged value that will not rebuild is a decode failure, not a
      // record. Falling through left `{"__dec":"x"}` as `{ __dec = "x" }`
      // here and on the phone while the server errored — and a Set or Dict
      // that fell through CRASHED the first operation on it, because the
      // shape reduction has no kind for either and checks nothing.
      if (alone && '__dec' in v) {
        if (typeof v.__dec !== 'string') {
          throw new Error('the value should be a decimal number, got `' + v.__dec + '`');
        }
        const d = parseDecString(v.__dec);
        if (!d) throw new Error('the value should be a decimal number, got `' + v.__dec + '`');
        return d;
      }
      // Dict / Set round-trip. The encoder emits `{__dict:[[k,v],...]}`
      // and `{__set:[i, ...]}`. We rebuild via the runtime's own
      // insert helpers so the sorted invariant survives even when the
      // wire payload arrives out of order (hand-written JSON, network
      // intermediaries, etc).
      if (alone && '__dict' in v) {
        if (!Array.isArray(v.__dict)) {
          throw new Error('the value should be a list of key/value pairs');
        }
        let d = VDict([]);
        for (const pair of v.__dict) {
          if (!Array.isArray(pair) || pair.length !== 2) {
            throw new Error('the value should be a key/value pair of two items');
          }
          try {
            d = dictInsertHelper(d, jsToMar(pair[0]), jsToMar(pair[1]));
          } catch (e) {
            // A dictionary is sorted by key, so building one compares the
            // keys, and mixed types failed that comparison with a message
            // about comparison rather than about the request.
            if (e && e.marWire) throw e;
            throw new Error('the value should be a list of key/value pairs whose keys are one type');
          }
        }
        return d;
      }
      if (alone && '__set' in v) {
        if (!Array.isArray(v.__set)) {
          throw new Error('the value should be a list of items');
        }
        let s = VSet([]);
        for (const it of v.__set) {
          try {
            s = setInsertHelper(s, jsToMar(it));
          } catch (e) {
            if (e && e.marWire) throw e;
            throw new Error('the value should be a list of items of one type');
          }
        }
        return s;
      }
      // Sorted, for the same reason marToJs sorts on the way out: a
      // decoded record has no source order to inherit, and the three
      // runtimes have to land on the same one. Go decodes through a map
      // and cannot see the document order at all, so sorted is the only
      // order all three can produce. `order` is what Display and the
      // missing-field error print.
      // Keys are strings too, so they are well-formed like any other.
      const fields = {};
      for (const raw of Object.keys(v)) fields[wellFormed(raw)] = jsToMar(v[raw]);
      const order = Object.keys(fields).sort(codePointCompare);
      return VRecord(fields, order);
    }
    return VString(String(v));
  }

  function marToJs(v) {
    if (!v || typeof v !== 'object') return v;
    switch (v.k) {
      case 'I': return v.n;
      case 'S': return v.s;
      case 'B': return v.b;
      case 'U': return null;
      case 'L': return v.xs.map(marToJs);
      case 'T': return v.xs.map(marToJs);
      case 'R': {
        // Sorted by field name in code point order, as the server and the
        // phone sort them, not by the order the literal was written. `order`
        // records where the fields appeared in the source, which is not part
        // of the value: `{ a = 1, b = 2 }` and `{ b = 2, a = 1 }` are equal,
        // and a pure function gives equal output for equal input.
        const out = {};
        for (const k of (v.order || Object.keys(v.fields)).slice().sort(codePointCompare)) out[k] = marToJs(v.fields[k]);
        return out;
      }
      case 'C':
        // Every ctor, Maybe and Result included, uses the `__ctor`
        // marker so generic decoders (jsToMar here, Go's convertJSON,
        // iOS MarJSONCodec) can rebuild a VCtor without needing type
        // info at runtime. Transparent encodings (Nothing → null,
        // Just x → x, Ok x → x, Err e → {error: e}) collide: with
        // VUnit's null, with `Just` of the inner value, with a record
        // that has an `error` field. The server's decode is untyped and
        // its request guard checks the tag, so the tag is what a Service
        // request has to carry.
        //
        // `__args` is written before `__ctor` because every object this
        // encoder emits has its keys in sorted order: see the record case.
        // The iOS encoder hands its tree to JSONSerialization, which can only
        // be made deterministic with `.sortedKeys`, so sorted is the one order
        // all three runtimes can agree on. Decoders look keys up, so none of
        // them notices.
        if (!v.args || v.args.length === 0) return { __ctor: v.tag };
        return { __args: v.args.map(marToJs), __ctor: v.tag };
      // `{"__duration": 1500}`, whole milliseconds; every decoder rebuilds
      // a Duration from it.
      case 'D': return { __duration: v.ms };
      case 'A': return { __angle: v.deci };
      case 'TM': return { __time: new Date(v.millis).toISOString() };
      case 'DT': return { __date: formatDateISO(v) };
      case 'Ch': return { __char: String.fromCodePoint(v.c) };
      case 'De': return { __dec: decStr(v) };
      case 'Dv': throw new Error('cannot encode a Decimal.Division — resolve it with Decimal.rounded or Decimal.withRemainder first');
      case 'M':
        return { __dict: v.pairs.map(p => [marToJs(p.key), marToJs(p.value)]) };
      case 'Se':
        return { __set: v.items.map(marToJs) };
      default:
        // Refuse rather than degrade. This arm used to answer null, so
        // `JSON.encode { f = \x -> x }` produced {"f":null} and a receiver
        // read "the field was null" when the field could not be written at
        // all. The server refuses these and so, now, does the phone; this side
        // was found last, by asking whether the fix on one runtime had a twin
        // here -- which is the same question the fix itself was about.
        throw new Error('JSON.encode: ' + marKindName(v) + ' has no JSON form.\n' +
          '  Encode the data it was built FROM, and build it on the side that uses it');
    }
  }

  // The Mar name for a value, for the sentence above. Keep these in step with
  // marKindOf in internal/runtime/json.go, which the same message uses.
  function marKindName(v) {
    switch (v && v.k) {
      case 'Fn':  return 'a function';
      case 'V':   return 'a View';
      case 'E':   return 'a Cmd or Task';
      case 'SND': return 'a Sound';
      case 'SUB': return 'a Sub';
      default:    return 'this value';
    }
  }

  // pathParamString renders a single value for a `{name:Type}` URL
  // segment. Mirrors the Go encodePathSegment: String raw, Int/Bool as
  // their literal, enum ctor lowercased.
  function pathParamString(v) {
    if (v && typeof v === 'object' && v.__ctor) return String(v.__ctor).toLowerCase();
    if (typeof v === 'boolean') return v ? 'true' : 'false';
    return String(v);
  }

  // buildServiceRequest turns a contract's verb + path pattern and the
  // request value into a concrete URL and (optionally) a JSON body. Typed
  // `{name:Type}` params are substituted into the URL; the remaining
  // fields ride in the query (`q` JSON param, for GET / DELETE) or the
  // JSON body (POST / PUT / PATCH). A `()` request yields no body. Whether
  // the request is a record is read off the Mar value: a constructor, a
  // Decimal or a Time also encode as JSON objects, and are sent whole.
  function buildServiceRequest(verb, pattern, req) {
    const obj = marToJs(req);
    const isObj = !!req && req.k === 'R';
    const fields = isObj ? Object.assign({}, obj) : {};
    let hadParam = false;
    const segs = pattern.split('/').filter(s => s.length).map(s => {
      const m = /^\{([^:}]+):[^}]*\}$/.exec(s);
      if (!m) return s;
      hadParam = true;
      const name = m[1];
      // The same encoder linkTo and Nav.* use. This built the URL its own
      // way, so `Service.call getNote { slug = "a'b" }` asked for
      // `/item/a'b` while `linkTo notePath { slug = "a'b" }` pointed at
      // `/item/a%27b` — one language, one job, two answers.
      const enc = encodePathComponent(pathParamString(fields[name]));
      delete fields[name];
      return enc;
    });
    const url = '/' + segs.join('/');
    const restKeys = Object.keys(fields);
    if (verb === 'GET' || verb === 'DELETE') {
      if (restKeys.length > 0) {
        return { url: url + '?q=' + encodeURIComponent(JSON.stringify(fields)), body: undefined };
      }
      return { url, body: undefined };
    }
    // POST / PUT / PATCH
    if (!isObj) {
      return { url, body: JSON.stringify(obj) };
    }
    if (restKeys.length === 0 && hadParam) {
      return { url, body: undefined };
    }
    return { url, body: JSON.stringify(fields) };
  }

  // --- Dict / Set helpers (hoisted to IIFE level so jsToMar can use
  // them during decode, before makeBuiltinEnv has installed builtins).
  //
  // `dictSearch` / `setSearch` return the insertion index (binary
  // search) plus a `found` flag. Sort key is cmpValues; runtime
  // throws if cmpValues hits a non-comparable Mar value.
  //
  // `found` needs no comparison of its own: keys are unique, so the
  // lower bound the loop lands on is the slot it last compared (the
  // last `hi = mid`), and that comparison already said whether it was
  // equal. When `hi` never moved, lo is the end and nothing matched.

  function dictSearch(d, key) {
    let lo = 0, hi = d.pairs.length, found = false;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const c = cmpValues(d.pairs[mid].key, key);
      if (c < 0) lo = mid + 1; else { hi = mid; found = c === 0; }
    }
    return { idx: lo, found };
  }

  // A copy of a sorted array with one element put in or taken out. Dict
  // and Set are persistent, so every insert and remove builds a new
  // array; what matters is how many times it copies. slice() then
  // splice() copies the whole array and then grows and shifts it again;
  // toSpliced (ES2023) makes the one copy, about 6x faster on a 5003-pair
  // Dict in node 26. An engine without it takes the two-step copy, which
  // builds the same array.
  const HAS_TO_SPLICED = typeof Array.prototype.toSpliced === 'function';
  function arrayWithInserted(xs, idx, x) {
    if (HAS_TO_SPLICED) return xs.toSpliced(idx, 0, x);
    const out = xs.slice();
    out.splice(idx, 0, x);
    return out;
  }
  function arrayWithRemoved(xs, idx) {
    if (HAS_TO_SPLICED) return xs.toSpliced(idx, 1);
    const out = xs.slice();
    out.splice(idx, 1);
    return out;
  }

  // dictPutAt stores `key -> value` at a slot dictSearch already found,
  // so Dict.update, which has to search before calling its function,
  // does not search a second time to store the answer.
  function dictPutAt(d, idx, found, key, value) {
    if (!found) return VDict(arrayWithInserted(d.pairs, idx, { key, value }));
    const pairs = d.pairs.slice();
    pairs[idx] = { key, value };
    return VDict(pairs);
  }
  function dictInsertHelper(d, key, value) {
    const { idx, found } = dictSearch(d, key);
    return dictPutAt(d, idx, found, key, value);
  }
  function dictRemoveHelper(d, idx) {
    return VDict(arrayWithRemoved(d.pairs, idx));
  }

  function setSearch(s, key) {
    let lo = 0, hi = s.items.length, found = false;
    while (lo < hi) {
      const mid = (lo + hi) >> 1;
      const c = cmpValues(s.items[mid], key);
      if (c < 0) lo = mid + 1; else { hi = mid; found = c === 0; }
    }
    return { idx: lo, found };
  }
  function setInsertHelper(s, item) {
    const { idx, found } = setSearch(s, item);
    if (found) return s;
    return VSet(arrayWithInserted(s.items, idx, item));
  }

  // Dict.fromList, Set.fromList and Set.map build a whole container at
  // once. Inserting the items one at a time copies the array once per
  // item, which is quadratic: a 20000-item Set.fromList took seconds.
  // Sorting once and dropping duplicates gives the same value, by the
  // same rule as the Go runtime's buildSetSorted / buildDictSorted:
  //
  //   - a Set keeps the FIRST of two equal items, as setInsertHelper
  //     does (it returns the set untouched when the item is there);
  //   - a Dict keeps the LAST pair for a key, key and value both, as
  //     dictInsertHelper does (it overwrites the pair it finds).
  //
  // "Equal" is cmpValues answering 0, which is wider than identical:
  // 1.5 and 1.50 are one key, so which one survives is visible.
  // Array.prototype.sort is stable (ES2019), so equal items stay in list
  // order. Only for the values of a checked program, whose items share
  // one comparable type, so no comparison can fail and WHICH pairs get
  // compared changes nothing. The wire decoder (jsToMar) keeps inserting
  // one at a time: there, whether a malformed payload is refused depends
  // on the comparisons insertion makes.
  //
  // Both take a FRESH array in list order, sort it in place and keep it.
  function setFromItems(items) {
    items.sort(cmpValues);
    let n = items.length === 0 ? 0 : 1;
    for (let i = 1; i < items.length; i++) {
      if (cmpValues(items[n - 1], items[i]) !== 0) items[n++] = items[i];
    }
    items.length = n;
    return VSet(items);
  }
  function dictFromPairs(pairs) {
    pairs.sort((a, b) => cmpValues(a.key, b.key));
    let n = pairs.length === 0 ? 0 : 1;
    for (let i = 1; i < pairs.length; i++) {
      if (cmpValues(pairs[n - 1].key, pairs[i].key) === 0) pairs[n - 1] = pairs[i];
      else pairs[n++] = pairs[i];
    }
    pairs.length = n;
    return VDict(pairs);
  }

  // ---------- Path patterns ----------
  //
  // Hoisted to IIFE level so both makeBuiltinEnv (linkTo / Nav.pushTo
  // / Nav.replaceTo) and mountPages (URL matcher for dynamic pages)
  // can share the same parser. Same shape, same caching, same error
  // wording on either side.

  // Custom-enum types registered by the module loader. Entries look
  // like { Role: ["Member", "Admin"] }. URL matchers consult this
  // to decode `{role:Role}` segments; linkTo / Nav.pushTo emit
  // lowercased ctor names back into URLs. Mirrors
  // internal/runtime/path.go's EnumTypes.
  const enumTypes = Object.create(null);

  // Path matcher for Page.dynamic / Page.dynamicProtected.
  // Pattern '/notes/{id:Int}' splits to segments [{lit:'notes'},
  // {param:'id', type:'Int'}]. Empty leading/trailing slashes are
  // stripped so '/' parses as []. Bare ':id' (legacy/Express-style)
  // is rejected to keep the error consistent with the Go side.
  function parsePathPattern(p) {
    const parts = p.split('/').filter(s => s !== '');
    const segs = [];
    const seen = {};
    for (const part of parts) {
      if (part.startsWith(':')) {
        throw new Error(`path "${p}": bare ":${part.slice(1)}" not supported. Use "{${part.slice(1)}:Type}".`);
      }
      if (part.startsWith('{') && part.endsWith('}')) {
        const inner = part.slice(1, -1);
        const colon = inner.indexOf(':');
        if (colon < 0) {
          throw new Error(`path "${p}": param "{${inner}}" requires a type, e.g. "{${inner}:String}" or "{${inner}:Int}".`);
        }
        const name = inner.slice(0, colon).trim();
        const type = inner.slice(colon + 1).trim();
        if (!name) {
          throw new Error(`path "${p}": empty param name in "{${inner}}".`);
        }
        // Built-ins resolve immediately; everything else has to be
        // a registered enum type. The registry may not be populated
        // yet at very early bootstrap, so the decoder/encoder also
        // re-check at use time: the typechecker is the authoritative
        // gate, this is a defensive fallback for handwritten patterns.
        if (type !== 'String' && type !== 'Int' && !enumTypes[type]) {
          throw new Error(`path "${p}": unknown type "${type}" for param "${name}". Allowed: String, Int, or a zero-arg enum type.`);
        }
        if (seen[name]) {
          throw new Error(`path "${p}": duplicate param "${name}".`);
        }
        seen[name] = true;
        segs.push({ kind: 'param', name, type });
        continue;
      }
      if (part.includes('{') || part.includes('}')) {
        throw new Error(`path "${p}": malformed segment "${part}" (use "{name:Type}").`);
      }
      segs.push({ kind: 'lit', value: part });
    }
    return { source: p, segments: segs };
  }
  // Decode a single URL segment per its declared type. Returns
  // VString / VInt / VCtor on success, null on type mismatch
  // (e.g. "abc" against `{id:Int}`, or "foo" against `{role:Role}`
  // where Role has no `Foo` ctor). Match failure surfaces as null
  // further up: the matcher tries the next page.
  function decodePathSegment(raw, type) {
    let decoded = raw;
    try { decoded = decodeURIComponent(raw); } catch (_) {}
    if (type === 'String') return VString(decoded);
    if (type === 'Int') {
      if (!/^-?\d+$/.test(decoded)) return null;
      const n = parseInt(decoded, 10);
      // 53 bits, the range a Mar Int has (ADR 0021). parseInt happily
      // ROUNDS past it — "9007199254740993" comes back as
      // …992 — so the browser matched the route and bound a number that
      // is not the one in the URL, while the phone bound the exact value
      // and the server (before this) returned it intact. One URL anyone
      // can type, three answers.
      if (!Number.isSafeInteger(n)) return null;
      return VInt(n);
    }
    // Custom enum: case-insensitive match against the registered
    // ctor list. Lowercased URL ↔ PascalCase ctor is the convention.
    const ctors = enumTypes[type];
    if (ctors) {
      const want = decoded.toLowerCase();
      for (const c of ctors) {
        if (c.toLowerCase() === want) return VCtor(c);
      }
      return null;
    }
    return null;
  }
  // Encode a VInt / VString / VCtor back to a URL segment. Used by
  // linkTo / Nav.pushTo. Type mismatches throw: caller surfaces
  // them at the call-site with the path source for context.
  // encodePathComponent percent-encodes one path SEGMENT, keeping only the
  // unreserved set of RFC 3986 (`A-Za-z0-9-._~`).
  //
  // Spelled out rather than borrowed from the host: `encodeURIComponent`
  // leaves `! ' ( ) * ~` alone, Swift's `.urlPathAllowed` leaves a SLASH
  // alone, and Go left everything alone. The three runtimes have to build
  // the same URL from the same value. Pinned by the corpus.
  function encodePathComponent(s) {
    let out = '';
    const bytes = new TextEncoder().encode(s);
    for (const b of bytes) {
      const c = String.fromCharCode(b);
      if (/[A-Za-z0-9\-._~]/.test(c)) { out += c; continue; }
      out += '%' + b.toString(16).toUpperCase().padStart(2, '0');
    }
    return out;
  }

  function encodePathSegment(v, type) {
    if (type === 'String') {
      if (!v || v.k !== 'S') throw new Error(`expected String, got ${v && v.k}`);
      return encodePathComponent(v.s);
    }
    if (type === 'Int') {
      if (!v || v.k !== 'I') throw new Error(`expected Int, got ${v && v.k}`);
      return String(v.n);
    }
    const ctors = enumTypes[type];
    if (ctors) {
      if (!v || v.k !== 'C') throw new Error(`expected ${type}, got ${v && v.k}`);
      // Defensive: ensure the ctor is actually a member. The
      // typechecker should already guarantee this, but a stray
      // runtime VCtor would otherwise produce a URL with arbitrary
      // tag names.
      for (const c of ctors) {
        if (c === v.tag) return v.tag.toLowerCase();
      }
      throw new Error(`ctor "${v.tag}" is not a member of ${type}`);
    }
    throw new Error(`unknown path-param type "${type}"`);
  }
  // Match a URL against a parsed pattern. Returns a VRecord with
  // typed fields on success, null on miss. Segment count must
  // match exactly, '/notes/{id:Int}' won't match '/notes' or
  // '/notes/abc/edit', and '/notes/abc' will miss '{id:Int}'.
  function matchPathPattern(urlPath, pattern) {
    const urlSegs = urlPath.split('/').filter(s => s !== '');
    if (urlSegs.length !== pattern.segments.length) return null;
    const params = {};
    const order = [];
    for (let i = 0; i < urlSegs.length; i++) {
      const seg = pattern.segments[i];
      const us = urlSegs[i];
      if (seg.kind === 'lit') {
        if (seg.value !== us) return null;
        continue;
      }
      const v = decodePathSegment(us, seg.type);
      if (v === null) return null;
      params[seg.name] = v;
      order.push(seg.name);
    }
    return VRecord(params, order);
  }
  // Build a URL string from a parsed pattern + a VRecord of params.
  // Used by linkTo and Nav.pushTo / Nav.replaceTo. Throws on
  // missing/wrong-type fields so user code gets a clear error
  // rather than a silently malformed URL.
  function buildPathURL(pattern, paramsRec) {
    let out = '';
    for (const seg of pattern.segments) {
      out += '/';
      if (seg.kind === 'lit') {
        out += seg.value;
        continue;
      }
      const v = (paramsRec && paramsRec.fields) ? paramsRec.fields[seg.name] : undefined;
      if (v === undefined) {
        throw new Error(`linkTo ${pattern.source}: missing param "${seg.name}"`);
      }
      try {
        out += encodePathSegment(v, seg.type);
      } catch (e) {
        throw new Error(`linkTo ${pattern.source}: param "${seg.name}": ${e.message}`);
      }
    }
    return out === '' ? '/' : out;
  }

  // ---------- Environment ----------

  // Two kinds of frame, one object shape. The builtins and each module get a
  // frame holding many names in `bindings`. Everything a call, a `let` or a
  // `case` binds gets a frame of its own holding ONE name in `name`/`val`,
  // with `bindings` null: those are made by the million, and a
  // `Object.create(null)` per parameter is a dictionary-mode object per
  // parameter, which made every lookup that walked past it a hash probe.
  // Lookup is unchanged: innermost frame first, and a name bound to
  // `undefined` is not found there.
  function envNew(parent) { return { name: null, val: undefined, bindings: Object.create(null), parent }; }
  function envBind(env, name, val) { return { name, val, bindings: null, parent: env }; }
  function envDefine(env, name, val) { env.bindings[name] = val; }
  function envLookup(env, name) {
    for (let cur = env; cur; cur = cur.parent) {
      const b = cur.bindings;
      if (b === null) {
        if (cur.name === name && cur.val !== undefined) return cur.val;
      } else {
        const v = b[name];
        if (v !== undefined) return v;
      }
    }
    return undefined;
  }

  // envExportsOf collects every binding that belongs to module
  // `modName`: keys of the form `modName.suffix` where suffix has no
  // further dot (so `Mar.Admin.x` exports from `Mar.Admin`, not from
  // `Mar`). Powers `import M exposing (..)` at load time, mirroring
  // the Go runtime's Env.ExportsOf. Walks frames outermost-first so
  // inner bindings win, matching envLookup's shadowing order.
  function envExportsOf(env, modName) {
    const prefix = modName + '.';
    const frames = [];
    for (let cur = env; cur; cur = cur.parent) frames.push(cur);
    const out = Object.create(null);
    for (let i = frames.length - 1; i >= 0; i--) {
      const b = frames[i].bindings;
      if (!b) continue;
      for (const name in b) {
        if (!name.startsWith(prefix)) continue;
        const suffix = name.slice(prefix.length);
        if (suffix === '' || suffix.indexOf('.') !== -1) continue;
        out[suffix] = b[name];
      }
    }
    return out;
  }

  // ---------- Native function helpers ----------

  function native(arity, fn) {
    return VFn(null, null, null, fn, arity, []);
  }

  // ---------- Apply ----------

  // Runaway recursion is stopped by Mar, not by the host.
  //
  // The browser already threw a catchable RangeError, so nothing crashed, but
  // the limit was whatever the browser had left, which is far more than an
  // iPhone can do. The same app on the same code recursed 2000 deep happily on
  // the web and died on the phone. The two client runtimes ship as ONE app, so
  // they get one budget, and it has to be the one the tighter of them can
  // actually honour: 512, measured on the Swift interpreter with the
  // optimisation settings the iOS template really uses.
  //
  // The counter is ambient rather than carried on the value (the Go runtime's
  // trick, needed there because its requests are concurrent). JavaScript is
  // single-threaded, so ambient is both correct and cheaper, and being
  // ambient is what makes it see recursion routed through a higher-order
  // builtin like List.foldl, where the loop lives in the runtime and no Mar
  // frame would carry anything.
  const MAX_CALL_DEPTH = 512;
  let callDepth = 0;
  function tooMuchRecursion() {
    return new Error('too much recursion: more than ' + MAX_CALL_DEPTH +
      ' nested calls. A function is calling itself without reaching a base case');
  }

  // apply hands a function value ONE more argument: a partial application
  // until the function has every argument it takes, then the call.
  //
  // The arguments gathered so far are copied by hand, not with
  // `fn.applied.concat([arg])`: concat takes V8's generic path (it has to
  // honour Symbol.isConcatSpreadable), and this line runs once per ARGUMENT of
  // every call. It was the most expensive line in the interpreter, 21-30% of
  // all samples on both benchmark programs.
  function apply(fn, arg) {
    if (fn.k !== 'Fn') throw new Error('apply: not a function: ' + JSON.stringify(fn));
    const applied = withArg(fn.applied, arg);
    if (applied.length < fn.arity) {
      return VFn(fn.params, fn.body, fn.env, fn.native, fn.arity, applied);
    }
    if (fn.native) return fn.native(applied);
    // The body is entered HERE, not through a helper: every Mar call keeps
    // the same number of host frames it always had (evalExpr -> apply ->
    // evalExpr). The guard below is MAX_CALL_DEPTH Mar calls, and the host
    // stack has to hold that many; a frame more per call made programs a few
    // calls short of the limit die with the host's "Maximum call stack size
    // exceeded" instead. evalApp and applyMany repeat these lines for the
    // same reason.
    let env = fn.env;
    for (let i = 0; i < fn.params.length; i++) {
      env = envBind(env, fn.params[i], applied[i]);
    }
    if (callDepth >= MAX_CALL_DEPTH) throw tooMuchRecursion();
    // try/finally rather than a plain decrement: an error unwinding past here
    // would otherwise leave the counter high, and every later evaluation would
    // start partway to the limit.
    callDepth++;
    try {
      return evalExpr(fn.body, env);
    } finally {
      callDepth--;
    }
  }

  // A fresh array: the arguments a partial application holds are never
  // mutated, and a native may keep the array it is called with (a
  // constructor keeps it as its args).
  function withArg(prev, arg) {
    const n = prev.length;
    if (n === 0) return [arg];
    const out = prev.slice();
    out.push(arg);
    return out;
  }

  // evalApp evaluates a call `f a b c`, which the parser nests one argument at
  // a time: EApp(EApp(EApp(f, a), b), c). Evaluating it node by node built a
  // partial application for every argument but the last; this walks the spine
  // once (cached on the node) and hands the arguments over together.
  //
  // The ORDER is the nested evaluation's, step for step: the function first,
  // then each argument left to right, and the call happens the moment the
  // function has every argument it takes, BEFORE the next argument is
  // evaluated (a function that returns a function is called, and then the
  // rest go to what it returned). A partial application has no effect to
  // reorder, so only the call points matter, and they are where they were.
  function evalApp(e, env) {
    const args = e._args || appSpine(e);
    let fn = evalExpr(e._head, env);
    const n = args.length;
    let i = 0;
    while (i < n) {
      const arg = evalExpr(args[i++], env);
      if (fn.k !== 'Fn') throw new Error('apply: not a function: ' + JSON.stringify(fn));
      const applied = withArg(fn.applied, arg);
      while (applied.length < fn.arity && i < n) applied.push(evalExpr(args[i++], env));
      if (applied.length < fn.arity) {
        return VFn(fn.params, fn.body, fn.env, fn.native, fn.arity, applied);
      }
      if (fn.native) {
        fn = fn.native(applied);
        continue;
      }
      // Entered inline, as in apply: see the note there.
      let fenv = fn.env;
      for (let p = 0; p < fn.params.length; p++) fenv = envBind(fenv, fn.params[p], applied[p]);
      if (callDepth >= MAX_CALL_DEPTH) throw tooMuchRecursion();
      callDepth++;
      try {
        fn = evalExpr(fn.body, fenv);
      } finally {
        callDepth--;
      }
    }
    return fn;
  }

  function appSpine(e) {
    const args = [];
    let head = e;
    while (head.kind === 'EApp') {
      args.push(head.arg);
      head = head.fn;
    }
    args.reverse();
    nodeCache(e, '_head', head);
    return nodeCache(e, '_args', args);
  }

  // nodeCache remembers something derived from an AST node ON the node, as a
  // NON-enumerable property: JSON.stringify, Object.keys and for-in never see
  // it, so these caches stay out of anything that looks at a node (an error
  // message that prints a value holding a closure, the dev dock). The older
  // caches set by plain assignment in evalExpr (_qn, _pn, _decoder, _caller,
  // _verify, _me) are still enumerable. Program nodes are never frozen; a
  // reload brings fresh nodes.
  function nodeCache(node, key, value) {
    Object.defineProperty(node, key, { value, writable: false, enumerable: false, configurable: false });
    return value;
  }

  // applyMany(fn, [a, b]) is apply(apply(fn, a), b), and likewise for three
  // arguments, without the partial application in between. The stdlib's
  // folds, indexedMap, Dict.foldl and the rest call a two- or three-argument
  // function once per element, and going through apply one argument at a
  // time builds a VFn for each partial and copies the argument array once
  // per argument, only to throw the partial away.
  //
  // When fn still needs at least that many arguments, the inner applies
  // could only have built partials, so all the arguments go in at once and
  // the rest is apply's own tail. Otherwise (fn is saturated sooner and
  // returns another function, or is not a function at all) it IS the nested
  // apply. Value, error and call depth come out the same either way: a
  // partial application never counts toward MAX_CALL_DEPTH. `args` must be
  // a fresh array: it can become the call's argument list.
  //
  // Its frame is kept no larger than apply's (the one-at-a-time fallback
  // lives in its own function), because this frame sits on the host stack
  // once per nested Mar call that runs through a fold, and the host stack
  // must not run out sooner than it did.
  function applyMany(fn, args) {
    if (fn.k !== 'Fn' || fn.applied.length + args.length > fn.arity) return applyOneByOne(fn, args);
    if (fn.applied.length !== 0) args = fn.applied.concat(args);
    if (args.length < fn.arity) {
      return VFn(fn.params, fn.body, fn.env, fn.native, fn.arity, args);
    }
    if (fn.native) return fn.native(args);
    // Entered inline, as in apply: see the note there.
    let env = fn.env;
    for (let i = 0; i < fn.params.length; i++) env = envBind(env, fn.params[i], args[i]);
    if (callDepth >= MAX_CALL_DEPTH) throw tooMuchRecursion();
    callDepth++;
    try {
      return evalExpr(fn.body, env);
    } finally {
      callDepth--;
    }
  }
  function applyOneByOne(fn, args) {
    for (let i = 0; i < args.length; i++) fn = apply(fn, args[i]);
    return fn;
  }

  // ---------- Pattern matching ----------

  // describeValue produces a short human-readable summary of a
  // runtime value: used in error messages where pasting the full
  // value would be too noisy (records with dozens of fields, deeply
  // nested ctors). Keeps the same format for every value kind so
  // the operator can match the error against the AST quickly.
  function describeValue(v) {
    if (v == null) return 'null';
    switch (v.k) {
      case 'I': return 'Int ' + v.n;
      case 'De': return 'Decimal ' + decStr(v);
      case 'Dv': return 'Decimal.Division';
      case 'S': return 'String ' + JSON.stringify(v.s);
      case 'B': return 'Bool ' + v.b;
      case 'U': return 'Unit';
      case 'L': return 'List[' + v.xs.length + ']';
      case 'T': return 'Tuple(' + v.xs.length + ')';
      case 'R': return 'Record{' + (v.order || Object.keys(v.fields)).join(',') + '}';
      case 'C': return 'Ctor ' + v.tag + (v.args && v.args.length ? '(' + v.args.length + ' arg)' : '');
      case 'Fn': return 'Fn arity=' + v.arity;
      case 'V': return 'View<' + v.tag + '>';
      case 'E': return 'Effect<' + (v.tag || '?') + '>';
      case 'D': return 'Duration ' + durationSecondsText(v.ms) + 's';
      case 'A': return 'Angle ' + (v.deci / 10) + '°';
      case 'TM': return 'Time ' + v.millis + 'ms';
      case 'DT': return 'Date ' + formatDateISO(v);
    }
    return 'unknown';
  }

  // matchInto matches v against pat. On success it returns env extended with
  // the pattern's variables, each in a frame of its own, bound in the order
  // the pattern lists them; on failure it returns null, and the frames it had
  // built are unreachable. A `case` used to fill an object for every branch it
  // TRIED and copy it into a fresh frame for the branch that matched.
  function matchInto(pat, v, env) {
    switch (pat.kind) {
      case 'PWildcard': return env;
      case 'PVar': return envBind(env, pat.name, v);
      case 'PInt': return v.k === 'I' && v.n === pat.value ? env : null;
      case 'PString': return v.k === 'S' && v.s === pat.value ? env : null;
      case 'PChar': return v.k === 'Ch' && v.c === pat.value ? env : null;
      case 'PUnit': return v.k === 'U' ? env : null;
      case 'PCtor':
        if (v.k !== 'C' || v.tag !== pat.name || v.args.length !== pat.args.length) return null;
        for (let i = 0; i < pat.args.length; i++) {
          env = matchInto(pat.args[i], v.args[i], env);
          if (env === null) return null;
        }
        return env;
      case 'PTuple':
        if (v.k !== 'T' || v.xs.length !== pat.members.length) return null;
        for (let i = 0; i < pat.members.length; i++) {
          env = matchInto(pat.members[i], v.xs[i], env);
          if (env === null) return null;
        }
        return env;
      case 'PList':
        if (v.k !== 'L' || v.xs.length !== pat.elements.length) return null;
        for (let i = 0; i < pat.elements.length; i++) {
          env = matchInto(pat.elements[i], v.xs[i], env);
          if (env === null) return null;
        }
        return env;
      case 'PCons':
        if (v.k !== 'L' || v.xs.length === 0) return null;
        env = matchInto(pat.head, v.xs[0], env);
        if (env === null) return null;
        return matchInto(pat.tail, VList(v.xs.slice(1)), env);
      case 'PRecord':
        // `{ f1, f2, ... }`: bind each listed field's value into
        // the scope. Partial-match: the value record may carry
        // additional fields the pattern doesn't list. The
        // typechecker already verified every listed field exists on
        // the value's static type, so a missing field here would be
        // a typechecker bug: treat it as a non-match rather than a
        // hard crash.
        //
        // hasOwnProperty (not `in`) so prototype keys like
        // `toString` / `hasOwnProperty` themselves can never satisfy
        // the lookup. v.fields is a plain object literal so its
        // prototype IS Object.prototype.
        if (v.k !== 'R') return null;
        for (let i = 0; i < pat.fields.length; i++) {
          const fname = pat.fields[i];
          if (!Object.prototype.hasOwnProperty.call(v.fields, fname)) return null;
          env = envBind(env, fname, v.fields[fname]);
        }
        return env;
    }
    return null;
  }

  // ---------- Eval ----------

  // ---------- Shape checking ----------
  //
  // A decoded value was not produced by the type system, and the code that
  // receives it was checked on the assumption that it has the declared
  // type. These two functions are the comparison nobody was making. They
  // are a port of CheckWire / CoerceWire in internal/runtime/wire.go, arm
  // for arm and sentence for sentence: three runtimes answering the same
  // question have to answer it the same way, and the corpus is what holds
  // them to it.

  function wireDescribe(v) {
    if (v === undefined || v === null) return 'nothing';
    switch (v.k) {
      case 'I': return 'a whole number';
      case 'De': return 'a number';
      case 'S': return 'a string';
      case 'B': return 'true or false';
      case 'Ch': return 'a character';
      case 'L': return 'a list';
      case 'U': return 'nothing';
      case 'TM': return 'a time';
      case 'DT': return 'a date';
      case 'D': return 'a duration';
      case 'C': return '`' + v.tag + '`';
      case 'R': return v.order.length === 0
        ? 'an empty object'
        : 'an object with ' + v.order.join(', ');
    }
    return 'something else';
  }

  function wireAbsent(v) {
    return v === undefined || v === null || v.k === 'U';
  }

  function quoteEach(tags) {
    return tags.map((t) => '`' + t + '`').join(', ');
  }

  function plural(n, noun) {
    return n === 1 ? '1 ' + noun : n + ' ' + noun + 's';
  }

  // Assigned when the builtins install, because it closes over the service
  // call itself. Declared here so the evaluator can see it: an assignment
  // with no declaration would be an implicit global, which is exactly the
  // kind of thing that works until the file is loaded in strict mode.
  let serviceCallWithShape = null;
  let authVerifyWithShape = null;
  let authMeWithShape = null;

  // A body from an auth endpoint, decoded and then held to the type the call
  // site asked for. Throws with the sentence Service.call gives, so the two
  // ports read the same when a server has moved on.
  function checkedMarUser(v, shape) {
    const err = checkWireAt(v, shape, '');
    if (err) throw wireMismatch('the response', err);
    return coerceWire(v, shape);
  }

  function checkedUser(text, shape) {
    return checkedMarUser(jsToMar(jsonBodyOrThrow(text)), shape);
  }

  // Returns null when the value fits the shape, or the sentence that says
  // how it does not.
  // `seen` carries the named shapes met on the way down, so a Ref can point
  // back at the type it is inside. A shape is a TREE and a recursive type is
  // not: the point where one re-entered itself used to be Any, checking
  // nothing from there down. Following the cycle against the DATA ends,
  // because the data is finite.
  function checkWireAt(v, s, path) {
    return checkWireIn(v, s, path, {});
  }

  function checkWireIn(v, s, path, seen) {
    const where = path === '' ? 'the value' : '`' + path + '`';
    const bad = (want) => where + ' should be ' + want + ', got ' + wireDescribe(v);

    switch (s.kind) {
      case 'Any': return null;
      case 'Int': return v.k === 'I' ? null : bad('a whole number');
      case 'String': return v.k === 'S' ? null : bad('a string');
      case 'Bool': return v.k === 'B' ? null : bad('true or false');
      case 'Char': return v.k === 'Ch' ? null : bad('a single character');
      case 'Decimal':
        // An Int is a legal Decimal on the wire: JSON has one number type.
        return (v.k === 'De' || v.k === 'I') ? null : bad('a number');
      case 'Time':
        return v.k === 'TM' ? null : bad('a time (an ISO-8601 string under "__time")');
      case 'Date':
        return v.k === 'DT' ? null : bad('a date (a YYYY-MM-DD string under "__date")');
      case '()':
        // Records ignore what they do not declare, so `()` does too: it is
        // one rule and the two halves used to disagree.
        return (v.k === 'U' || v.k === 'R') ? null : bad('empty');

      case 'Maybe': {
        if (wireAbsent(v)) return null;
        if (v.k !== 'C') return bad('a Maybe (leave the field out for Nothing)');
        if (v.tag === 'Nothing') return null;
        if (v.tag === 'Just' && v.args.length === 1) {
          return s.elem ? checkWireIn(v.args[0], s.elem, path, seen) : null;
        }
        return bad('a Maybe (leave the field out for Nothing)');
      }

      case 'Ctor': {
        // Remembered under its type name so a Ref below can find it.
        // A fresh object keeps sibling branches independent.
        if (s.name) seen = Object.assign({}, seen, { [s.name]: s });
        const tags = s.tags || [];
        if (v.k !== 'C') return bad('one of ' + quoteEach(tags));
        const i = tags.indexOf(v.tag);
        if (i < 0) {
          return where + ' should be one of ' + quoteEach(tags) + ', got `' + v.tag + '`';
        }
        // The right tag still has to carry the right things: a
        // constructor's arguments are part of its type.
        const want = (s.args && s.args[i]) || [];
        if (v.args.length !== want.length) {
          return where + ': `' + v.tag + '` takes ' + plural(want.length, 'value') +
            ', got ' + v.args.length;
        }
        for (let j = 0; j < v.args.length; j++) {
          const err = checkWireIn(v.args[j], want[j], path + '.' + v.tag + '[' + j + ']', seen);
          if (err) return err;
        }
        return null;
      }

      case 'Ref': {
        // The type this position re-enters, remembered on the way down. A
        // Ref with nothing to point at came from a bundle older than this
        // kind: it asserts nothing rather than refusing.
        const target = seen[s.name];
        return target ? checkWireIn(v, target, path, seen) : null;
      }
      case 'Set': {
        // Set, Dict and Angle had no arm here, the same hole the tuple had:
        // measured on the server, a plain `[1,2,3]` where the type says
        // `Set Int` sailed through the guard and answered 500 with
        // `Set.toList: not a Set`.
        if (v.k !== 'Se') return bad('a set');
        for (let i = 0; i < v.items.length; i++) {
          const err = checkWireIn(v.items[i], s.elem, path + '[' + i + ']', seen);
          if (err) return err;
        }
        return null;
      }
      case 'Dict': {
        if (v.k !== 'M') return bad('a dictionary');
        const members = s.members || [];
        for (let i = 0; i < v.pairs.length; i++) {
          const kErr = checkWireIn(v.pairs[i].key, members[0], path + '[' + i + '].key', seen);
          if (kErr) return kErr;
          const vErr = checkWireIn(v.pairs[i].value, members[1], path + '[' + i + '].value', seen);
          if (vErr) return vErr;
        }
        return null;
      }
      case 'Angle':
        return v.k === 'A' ? null : bad('an angle');
      case 'Duration':
        return v.k === 'D' ? null : bad('a duration (whole milliseconds under "__duration")');
      case 'Tuple': {
        // A tuple travels as a JSON array and comes back as a LIST: an array
        // is all the wire has. Both forms arrive here, and the arity is what
        // separates a well-formed tuple from anything else.
        const members = s.members || [];
        if (v.k !== 'T' && v.k !== 'L') return bad('a list of ' + plural(members.length, 'value'));
        if (v.xs.length !== members.length) {
          return where + ' should be a list of ' + plural(members.length, 'value') +
            ', got ' + v.xs.length;
        }
        for (let i = 0; i < v.xs.length; i++) {
          const err = checkWireIn(v.xs[i], members[i], path + '[' + i + ']', seen);
          if (err) return err;
        }
        return null;
      }

      case 'List': {
        if (v.k !== 'L') return bad('a list');
        if (!s.elem) return null;
        for (let i = 0; i < v.xs.length; i++) {
          const err = checkWireIn(v.xs[i], s.elem, path + '[' + i + ']', seen);
          if (err) return err;
        }
        return null;
      }

      case 'Record': {
        if (v.k !== 'R') return bad('an object');
        const fields = s.fields || [];
        for (const f of fields) {
          if (!Object.prototype.hasOwnProperty.call(v.fields, f.name)) {
            // A missing Maybe is Nothing; a missing anything else is the
            // single most common wire bug, so it gets its own message.
            if (f.shape.kind === 'Maybe') continue;
            return where + ' is missing the field `' + f.name + '`';
          }
          const err = checkWireIn(v.fields[f.name], f.shape, path === '' ? f.name : path + '.' + f.name, seen);
          if (err) return err;
        }
        // Extra fields are allowed on purpose: a newer client sending a
        // field this build does not know about should not be a rejection.
        return null;
      }
    }
    return null;
  }

  // Accepting a value means producing the value that was accepted: a JSON
  // 3 for a Decimal field is legal and has to arrive as a Decimal, and a
  // declared optional field nobody sent still has to BE there, because the
  // code reads it whether or not it was written.
  // seen remembers each named type on the way down, so a Ref (a recursive
  // type re-entering itself) coerces like the type it names, as checkWireIn
  // does: in `Link ( 1, 2 ) (Link ( 3, 4 ) End)` both pairs are tuples.
  // coerceWireIn in internal/runtime/wire.go is the same function.
  function coerceWire(v, s, seen) {
    seen = seen || {};
    switch (s.kind) {
      case 'Decimal':
        return v.k === 'I' ? VDec(BigInt(v.n), 0) : v;
      case 'Ref': {
        const target = seen[s.name];
        return target ? coerceWire(v, target, seen) : v;
      }
      case 'Tuple': {
        // The decoder handed back a list; the code was checked against a
        // tuple. Accepting the array means producing the tuple.
        const members = s.members || [];
        if ((v.k !== 'T' && v.k !== 'L') || v.xs.length !== members.length) return v;
        return VTuple(v.xs.map((x, i) => coerceWire(x, members[i], seen)));
      }
      case 'Ctor': {
        if (s.name) seen = Object.assign({}, seen, { [s.name]: s });
        if (v.k !== 'C') return v;
        const i = (s.tags || []).indexOf(v.tag);
        const want = (s.args && s.args[i]) || [];
        if (i < 0 || v.args.length !== want.length) return v;
        return VCtor(v.tag, v.args.map((a, j) => coerceWire(a, want[j], seen)));
      }
      case 'Maybe':
        if (wireAbsent(v)) return VCtor('Nothing', []);
        if (v.k === 'C' && v.tag === 'Just' && v.args.length === 1 && s.elem) {
          return VCtor('Just', [coerceWire(v.args[0], s.elem, seen)]);
        }
        return v;
      case 'List':
        if (v.k !== 'L' || !s.elem) return v;
        return VList(v.xs.map((x) => coerceWire(x, s.elem, seen)));
      // A Set or a Dict holds values of its own types, so they are coerced
      // too: a tuple key arrives as the List the decoder made, and a
      // container's operations compare its items by kind. The items change
      // kind, and their order is the comparison's, so the container is built
      // again, by the same rule Set.fromList and Dict.fromList use.
      case 'Set':
        if (v.k !== 'Se' || !s.elem) return v;
        return setFromItems(v.items.map((x) => coerceWire(x, s.elem, seen)));
      case 'Dict': {
        const members = s.members || [];
        if (v.k !== 'M' || members.length !== 2) return v;
        return dictFromPairs(v.pairs.map((p) => ({
          key: coerceWire(p.key, members[0], seen),
          value: coerceWire(p.value, members[1], seen),
        })));
      }
      case 'Record': {
        if (v.k !== 'R') return v;
        const fields = Object.assign({}, v.fields);
        const order = v.order.slice();
        for (const f of (s.fields || [])) {
          if (!Object.prototype.hasOwnProperty.call(fields, f.name)) {
            if (f.shape.kind === 'Maybe') {
              fields[f.name] = VCtor('Nothing', []);
              order.push(f.name);
            }
            continue;
          }
          fields[f.name] = coerceWire(fields[f.name], f.shape, seen);
        }
        return VRecord(fields, order);
      }
    }
    return v;
  }

  // JSON.decode at a call site whose result type the checker knew. Same
  // decoder, one more question asked afterwards, and the answer goes where
  // a decode failure already went: Err.
  function decodeWithShape(shape) {
    return native(1, ([raw]) => {
      let parsed;
      try {
        parsed = parseJSONExact(raw.s);
      } catch (e) {
        // Mar's sentence, not V8's: see internal/runtime/json.go. The other
        // failures below are already Mar's own and match across runtimes.
        return VCtor('Err', [VString('the document is not valid JSON')]);
      }
      // jsToMar is inside the guard too. It rejects a tagged value that
      // cannot be one — `{"__char":"abc"}` is not a character — and that
      // is a decode failure like any other, so it goes where decode
      // failures go rather than escaping as a thrown Error.
      let v;
      try {
        v = jsToMar(parsed);
      } catch (e) {
        return VCtor('Err', [VString(String((e && e.message) || e))]);
      }
      const err = checkWireAt(v, shape, '');
      if (err) return VCtor('Err', [VString(err)]);
      return VCtor('Ok', [coerceWire(v, shape)]);
    });
  }

  function evalExpr(e, env) {
    switch (e.kind) {
      case 'EInt':    return VInt(e.value);
      // coef ships as a string (34 digits overflow Number): see the
      // serializer note in internal/jsserve/serialize.go.
      case 'EDecimal': return VDec(BigInt(e.coef), e.scale);
      case 'EString': return VString(e.value);
      case 'EChar':   return VChar(e.value);
      case 'EUnit':   return VUnit();
      case 'EVar': {
        if (e.impl !== undefined) {
          const chosen = envLookup(env, e.impl);
          if (chosen !== undefined) return chosen;
        }
        const v = envLookup(env, e.name);
        if (v === undefined) throw marInternal('unbound name: ' + e.name);
        return v;
      }
      case 'ECtor': {
        // Qualified or bare constructor. The full key (`Module.Sub.Name`)
        // is the same for every evaluation of this AST node, so memoize
        // it on the node itself the first time we build it.
        const key = e._qn || (e._qn = (e.module && e.module.length > 0
          ? e.module.join('.') + '.' + e.name
          : e.name));
        let v = envLookup(env, key);
        if (v === undefined) v = envLookup(env, e.name);
        // No guess-the-tag fallback here, deliberately. Every qualified
        // builtin constructor is registered by the generated region (see
        // GENERATED BUILTIN CTORS; internal/ctorgen keeps it in sync with
        // the typecheck tables), and user constructors are registered
        // bare AND qualified by the module loader's Pass 1. A miss now
        // means a registration bug, and it should be LOUD: the old
        // fallback (return VCtor(e.name)) let this class of bug hide for
        // weeks by silently manufacturing plausible values.
        if (v === undefined) throw marInternal('unbound constructor: ' + key);
        return v;
      }
      case 'EQualified': {
        // `impl` is the implementation the typechecker chose for THIS
        // occurrence (see ast.EQualified.Impl): today only a Decimal
        // List.sum / List.product, whose empty-list zero the values
        // cannot reveal.
        if (e.impl !== undefined) {
          const chosen = envLookup(env, e.impl);
          if (chosen !== undefined) return chosen;
        }
        // `wire` is the shape this occurrence needs, written by the checker
        // because the call site's type is the only place it exists. What to
        // DO with it depends on which builtin this is, so the NAME is
        // checked and not merely the mark: JSON.decode wants a decoder that
        // validates, Service.call wants its RESPONSE validated. Reading one
        // as the other would replace a network call with a JSON parser.
        // Absent on every other node, and absent on an older bundle, where
        // this falls through to the plain lookup and behaves as it did.
        const key = e._qn || (e._qn = e.module.join('.') + '.' + e.name);
        if (e.wire !== undefined && key === 'JSON.decode') {
          return e._decoder || (e._decoder = decodeWithShape(e.wire));
        }
        if (e.wire !== undefined && key === 'Service.call' && serviceCallWithShape) {
          return e._caller || (e._caller = serviceCallWithShape(e.wire));
        }
        if (e.wire !== undefined && key === 'Auth.verifyCode' && authVerifyWithShape) {
          return e._verify || (e._verify = authVerifyWithShape(e.wire));
        }
        if (e.wire !== undefined && key === 'Auth.me' && authMeWithShape) {
          return e._me || (e._me = authMeWithShape(e.wire));
        }
        let v = envLookup(env, key);
        if (v === undefined) v = envLookup(env, e.name);
        if (v === undefined) throw marInternal('unbound name: ' + key);
        return v;
      }
      case 'ENegate': {
        const v = evalExpr(e.inner, env);
        if (v.k === 'I') return VInt(-v.n);
        if (v.k === 'De') return VDec(-v.coef, v.scale);
        throw new Error('negate: unsupported type');
      }
      case 'EApp': {
        // One argument: the original three lines, frame for frame. Only a
        // call written with several arguments (`f a b`) goes through evalApp.
        if (e.fn.kind !== 'EApp') {
          const fn = evalExpr(e.fn, env);
          const arg = evalExpr(e.arg, env);
          return apply(fn, arg);
        }
        return evalApp(e, env);
      }
      case 'EBinop': {
        // The operator's implementation, from BINOPS (cached on the node: it
        // depends on the operator alone). One BINOPS does not know takes the
        // general path, which looks it up and reports it.
        const impl = e._impl !== undefined ? e._impl : nodeCache(e, '_impl', BINOPS[e.op] || null);
        const op = impl !== null ? null : envLookup(env, e.op);
        if (impl === null && op === undefined) throw new Error('unknown operator: ' + e.op);
        const left = evalExpr(e.left, env);
        // `&&` and `||` SHORT-CIRCUIT: when the left operand already decides the
        // answer, the right one is never evaluated. Mirrors evalAt in
        // internal/runtime/eval.go and Eval.eval in MarEval.swift.
        //
        // Not an optimisation. Mar is pure but not total, so an operand can
        // fail: `n < 1 && 9007199254740991 + n > 0` used to overflow with the
        // left side already False, and a recursion guarded by `depth < 80 && …`
        // used to spend the stack anyway. Elm short-circuits; so does this now.
        if (e.op === '&&' || e.op === '||') {
          if (left.k !== 'B') throw marInternal('an operand of `' + e.op + '` evaluated to something that is not a Bool');
          if (e.op === '&&' && !left.b) return VBool(false);
          if (e.op === '||' && left.b) return VBool(true);
          const rv = evalExpr(e.right, env);
          if (rv.k !== 'B') throw marInternal('an operand of `' + e.op + '` evaluated to something that is not a Bool');
          return rv;
        }
        const right = evalExpr(e.right, env);
        if (impl !== null) return impl(left, right);
        return apply(apply(op, left), right);
      }
      case 'ELambda': {
        // Param names depend only on the AST node (immutable), so cache
        // the array on the node itself instead of remapping every call.
        const paramNames = e._pn || (e._pn = e.params.map(p => {
          if (p.kind === 'PVar') return p.name;
          if (p.kind === 'PWildcard') return '__wild';
          throw new Error('lambda params must be names or _');
        }));
        return VFn(paramNames, e.body, env, null, paramNames.length, []);
      }
      case 'EIf': {
        const c = evalExpr(e.cond, env);
        return c.b ? evalExpr(e.then, env) : evalExpr(e.else, env);
      }
      case 'ELet': {
        let cur = env;
        for (const b of e.bindings) {
          const val = evalExpr(b.body, cur);
          const next = matchInto(b.pattern, val, cur);
          if (next !== null) cur = next;
        }
        return evalExpr(e.body, cur);
      }
      case 'ETuple':
        return VTuple(e.members.map(m => evalExpr(m, env)));
      case 'EList':
        return VList(e.elements.map(x => evalExpr(x, env)));
      case 'ERecord': {
        const fields = {};
        const order = [];
        for (const f of e.fields) {
          fields[f.name] = evalExpr(f.value, env);
          order.push(f.name);
        }
        return VRecord(fields, order);
      }
      case 'ERecordUpdate': {
        const base = evalExpr(e.record, env);
        if (base.k !== 'R') throw new Error('record update on non-record');
        const fields = Object.assign({}, base.fields);
        for (const f of e.fields) fields[f.name] = evalExpr(f.value, env);
        return VRecord(fields, base.order);
      }
      case 'EFieldAccess': {
        const r = evalExpr(e.record, env);
        if (r.k !== 'R') throw new Error('field access on non-record');
        const v = r.fields[e.field];
        if (v === undefined) missingField(r, e.field);
        return v;
      }
      case 'EFieldAccessor':
        return native(1, args => {
          const rec = args[0];
          if (rec.k !== 'R') throw new Error('field accessor on non-record');
          const v = rec.fields[e.field];
          if (v === undefined) missingField(rec, e.field);
          return v;
        });
      case 'ECase': {
        const subj = evalExpr(e.subject, env);
        for (const br of e.branches) {
          const frame = matchInto(br.pattern, subj, env);
          if (frame !== null) return evalExpr(br.body, frame);
        }
        // Include the subject's shape in the error so the
        // browser console points at WHICH case match exploded.
        // A bare "no case branch matched" forces the operator to
        // bisect by guesswork; with the tag/kind we usually
        // spot the missing branch in seconds.
        //
        // Tagged so the MVU dispatch boundary can tell this specific
        // failure apart from any other throw: because `case`
        // exhaustiveness is checked at compile time, a page's update can
        // ALWAYS match its own messages, so a no-branch failure reaching
        // dispatch can only be a stale message meant for a torn-down page.
        const noBranchErr = new Error('no case branch matched (subject: ' + describeValue(subj) + ')');
        noBranchErr.marNoCaseBranch = true;
        throw noBranchErr;
      }
    }
    throw new Error('unsupported expr: ' + e.kind);
  }

  // ---------- The deploy moved under an open tab ----------
  //
  // A page load pins runtime.js and the program together: the HTML
  // carries the program inline and both revalidate on every load, so a
  // fresh load is always internally consistent. What a fresh load
  // cannot fix is the tab that never reloads. Leave an app open, deploy
  // twice, and that tab is still running last week's code while its
  // service calls land on today's server.
  //
  // The server already says who it is on every single response (see
  // internal/jsserve/version_headers.go):
  //
  //   X-Mar-Program  hash of the program.json being served
  //   X-Mar-Runtime  the mar version that built the server
  //
  // So the check is just: remember what we saw first, and notice when a
  // later response disagrees. No polling, no extra request: the
  // evidence rides along with traffic the app was making anyway.
  //
  // The two headers differ in what they mean, and the difference is
  // worth keeping even though the remedy is the same reload:
  //
  //   program changed  the app's own code was redeployed. The running
  //                    page still works; it is just old.
  //   runtime changed  the framework itself changed under us, so the
  //                    wire format this page speaks may no longer be
  //                    the one the server answers in. Service calls can
  //                    start failing in ways that look like app bugs.
  //
  // Never in `mar dev`: hot reload owns that case, and every save
  // changes the program hash, so the bar would be permanent furniture.
  let seenProgramHash = null;
  let seenRuntimeVersion = null;
  let staleKind = null;      // null | 'program' | 'runtime'

  function noteServerIdentity(resp) {
    if (!resp || typeof resp.headers === 'undefined') return;
    if (global.__marDevMode) return;
    let program = null, runtime = null;
    try {
      program = resp.headers.get('X-Mar-Program');
      runtime = resp.headers.get('X-Mar-Runtime');
    } catch (_) {
      // Opaque response (cross-origin, no CORS expose): headers are
      // unreadable rather than absent. Nothing to compare.
      return;
    }
    // A response with neither header is not this server talking: an
    // older build, or a proxy that drops what it does not recognise.
    // Silence is not evidence of a change.
    if (runtime) {
      if (seenRuntimeVersion === null) seenRuntimeVersion = runtime;
      else if (runtime !== seenRuntimeVersion) markStale('runtime');
    }
    if (program) {
      if (seenProgramHash === null) seenProgramHash = program;
      else if (program !== seenProgramHash) markStale('program');
    }
  }

  // A runtime change outranks a program change and cannot be downgraded:
  // once the framework has moved, learning that the app code also moved
  // does not make the situation less serious.
  function markStale(kind) {
    if (staleKind === 'runtime') return;
    if (staleKind === kind) return;
    staleKind = kind;
    showStaleDeployBar();
  }

  // A bar, not a modal. The page still works: it is running a complete,
  // internally consistent version of the app, just not the newest one:
  // and interrupting someone mid-form to tell them about a deploy would
  // cost more than it saves. Appended to <body> rather than into
  // #mar-root so the next render does not wipe it.
  function showStaleDeployBar() {
    if (typeof document === 'undefined' || !document.body) return;
    let bar = document.getElementById('mar-stale-deploy');
    if (!bar) {
      bar = document.createElement('div');
      bar.id = 'mar-stale-deploy';
      bar.setAttribute('role', 'status');
      const label = document.createElement('span');
      label.id = 'mar-stale-deploy-text';
      bar.appendChild(label);
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.textContent = 'Reload';
      // location.reload() rather than a cache-busting dance: every
      // asset is served no-cache with an ETag, so a plain reload
      // already revalidates and picks up the new deploy.
      btn.addEventListener('click', () => { window.location.reload(); });
      bar.appendChild(btn);
      document.body.appendChild(bar);
    }
    const text = document.getElementById('mar-stale-deploy-text');
    if (text) {
      text.textContent = staleKind === 'runtime'
        ? 'This page is out of date. Reload to keep going.'
        : 'A new version is available.';
    }
  }

  // ---------- Builtins ----------

  function eqValues(a, b) {
    if (a.k !== b.k) return false;
    switch (a.k) {
      case 'I': return a.n === b.n;
      // Numeric equality: 1.50 == 1.5 (scale is display metadata).
      case 'De': return decCmpV(a, b) === 0;
      case 'S': return a.s === b.s;
      case 'Ch': return a.c === b.c;
      // Every constructor wraps into 0..3599, so equality on the
      // representation IS equality of the rotation. Load-bearing: an Angle
      // lives in models, and models go through time travel, App.shared and
      // (in lendas) a rules engine replayed on both sides.
      case 'A': return a.deci === b.deci;
      // A Duration is milliseconds, so Time.seconds 60 and Time.minutes 1
      // ARE the same interval. A Time is the same moment when it is the
      // same Unix millisecond.
      case 'D': return a.ms === b.ms;
      case 'TM': return a.millis === b.millis;
      case 'DT': return a.days === b.days;
      case 'B': return a.b === b.b;
      case 'U': return true;
      case 'C':
        if (a.tag !== b.tag || a.args.length !== b.args.length) return false;
        for (let i = 0; i < a.args.length; i++) if (!eqValues(a.args[i], b.args[i])) return false;
        return true;
      case 'L':
        if (a.xs.length !== b.xs.length) return false;
        for (let i = 0; i < a.xs.length; i++) if (!eqValues(a.xs[i], b.xs[i])) return false;
        return true;
      case 'T':
        for (let i = 0; i < a.xs.length; i++) if (!eqValues(a.xs[i], b.xs[i])) return false;
        return true;
      case 'R': {
        // By FIELD SET, never by `order`: the order is display metadata (it
        // decides how a record renders), not identity, so { a = 1, b = 2 } and
        // { b = 2, a = 1 } are the same value. Mirrors equalValues (Go) and
        // equalsMar (Swift).
        //
        // This case was missing, and its absence was invisible: a record fell
        // through the whole switch to the `return false` at the bottom, so EVERY
        // record comparison in the browser answered "different": including a
        // value against itself, while the server and iOS answered correctly.
        // It reached further than `==`, because the container cases above recurse
        // through here: a record inside a Just, a list, or a tuple poisoned those
        // too, and so did List.member and the picker's selected-option lookup.
        const an = Object.keys(a.fields);
        if (an.length !== Object.keys(b.fields).length) return false;
        for (const n of an) {
          if (!Object.prototype.hasOwnProperty.call(b.fields, n)) return false;
          if (!eqValues(a.fields[n], b.fields[n])) return false;
        }
        return true;
      }
      case 'M':
        if (a.pairs.length !== b.pairs.length) return false;
        for (let i = 0; i < a.pairs.length; i++) {
          if (!eqValues(a.pairs[i].key, b.pairs[i].key)) return false;
          if (!eqValues(a.pairs[i].value, b.pairs[i].value)) return false;
        }
        return true;
      case 'Se':
        if (a.items.length !== b.items.length) return false;
        for (let i = 0; i < a.items.length; i++) {
          if (!eqValues(a.items[i], b.items[i])) return false;
        }
        return true;
    }
    return false;
  }

  // Ordering, in the same arm order as eqValues above. Two things here are
  // deliberate. It answers -1/0/1 rather than a difference, so every arm is
  // the same shape as every other and a caller cannot come to depend on the
  // magnitude. And the floor THROWS: it used to answer 0, which reads as
  // "these are equal" — a lie that Dict and Set act on, merging distinct
  // keys and losing entries with nothing reported anywhere. Records,
  // constructors and functions have no order, and the type checker's
  // Comparable constraint is what keeps them from arriving here.
  // Strings compare by CODE POINT, which is not what `<` does. JavaScript
  // orders by UTF-16 unit, and a leading surrogate (D800-DBFF) sits below
  // E000-FFFF, so `<` alone puts every character above the BMP under the
  // replacement character. ADR 0038 clause 5. It reaches further than
  // List.sort: `<` is what orders Dict iteration and Set contents.
  //
  // One order-preserving remap is enough. Surrogates move above the BMP
  // (+0x2800 puts D800-DFFF at 10000-107FF), which is where the characters
  // they encode belong; nothing else moves. Almost every comparison takes the
  // fast path, which is two native scans and a native compare: when neither
  // string holds a surrogate, UTF-16 order already IS code point order. That
  // path measures 31ns against 15ns for a bare `<`, on short ASCII keys in
  // node 26 - worth knowing because Dict and Set binary-search with this, so
  // it is on the hot path of every keyed lookup.
  const SURROGATE = /[\uD800-\uDFFF]/;
  function cmpStrings(a, b) {
    if (a === b) return 0;
    if (!SURROGATE.test(a) && !SURROGATE.test(b)) return a < b ? -1 : 1;
    const n = a.length < b.length ? a.length : b.length;
    for (let i = 0; i < n; i++) {
      let ca = a.charCodeAt(i), cb = b.charCodeAt(i);
      if (ca === cb) continue;
      if (ca >= 0xD800 && ca <= 0xDFFF) ca += 0x2800;
      if (cb >= 0xD800 && cb <= 0xDFFF) cb += 0x2800;
      return ca < cb ? -1 : 1;
    }
    return a.length < b.length ? -1 : a.length > b.length ? 1 : 0;
  }

  function cmpValues(a, b) {
    if (a.k !== b.k) throw new Error('comparison: type mismatch');
    switch (a.k) {
      case 'I': return a.n < b.n ? -1 : a.n > b.n ? 1 : 0;
      case 'De': return decCmpV(a, b);
      case 'S': return cmpStrings(a.s, b.s);
      case 'Ch': return a.c < b.c ? -1 : a.c > b.c ? 1 : 0;
      case 'T': {
        // Component-wise, left to right, first difference wins. Two tuples
        // that reach here always have the same arity: different arities are
        // different types and never unify.
        for (let i = 0; i < a.xs.length; i++) {
          const c = cmpValues(a.xs[i], b.xs[i]);
          if (c !== 0) return c;
        }
        return 0;
      }
      case 'L': {
        // Lexicographic, and a proper prefix is less than what extends it:
        // [1] < [1,2]. The same rule a dictionary uses for words.
        const n = Math.min(a.xs.length, b.xs.length);
        for (let i = 0; i < n; i++) {
          const c = cmpValues(a.xs[i], b.xs[i]);
          if (c !== 0) return c;
        }
        return a.xs.length < b.xs.length ? -1 : a.xs.length > b.xs.length ? 1 : 0;
      }
    }
    throw new Error('comparison: unsupported types');
  }

  // preservedModel / preservedScreenModels survive across marReload (the
  // IIFE wrapping this file runs once per page load, not per marRun call).
  // mountApp / mountScreens read them on entry to restore the user's last
  // state instead of always starting from init. Discarded if the new view
  // function rejects the old shape.
  let preservedModel = null;
  let preservedScreenModels = {};
  // Same idea for shared stores, keyed by the order in which their defs were
  // built. A def is a top-level binding, so the Nth App.shared call is the
  // same def on every reload; adding or removing one shifts the keys and
  // resets the stores, which is the same discipline a page model already has
  // when its shape changes.
  let preservedSharedModels = {};
  let sharedDefSeq = 0;

  // Time-travel state survives marReload too. After hot-reload, frames are
  // validated against the new view functions: any frame whose nextModel
  // can't be rendered by the (possibly updated) view of its page is
  // discarded. Empty list = no usable history → fresh start.
  //
  // currentJumpToFrame is updated by every mountPages call so the
  // module-level keyboard handler (registered once below) always
  // dispatches into the freshest closure. Without this indirection,
  // re-registering the listener on each hot-reload would multiply it
  // and arrow keys would skip N frames at a time.
  let preservedTimeTravel = null;
  let currentJumpToFrame = null;
  // Set by every mountPages call to the live page's render(). The dev dock
  // calls it when the time-travel panel opens/closes so the clock-sub freeze
  // (see render) applies at once, critical on CLOSE: while paused no timer is
  // firing, so without an explicit re-render nothing would ever un-freeze.
  let currentDevRerender = null;
  // LIVE open-state of the time-travel panel this session (the dock's expand /
  // collapse set it). The clock-freeze reads THIS, not localStorage: the
  // persisted "last expanded panel" survives reloads, and gating the pause on
  // it froze a fresh page (paused → no ticks → no frames → the panel's badge
  // never even shows → a silent, permanent freeze). Defaults false so a reload
  // always starts running; you pause only by actually opening the panel now.
  let timeTravelPanelIsOpen = false;
  if (__MAR_DEV__) {
    preservedTimeTravel = {
      frames: [],   // [{ msg, prevModel, nextModel, hadEffect, pagePath, time }]
      cursor: -1,
      traveling: false,
      total: 0,        // count of ALL actions ever (badge shows this; keeps growing)
    };
    if (typeof document !== 'undefined') {
      document.addEventListener('keydown', function (e) {
        const tag = (e.target && e.target.tagName) || '';
        if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return;
        if (!timeTravelPanelIsOpen) return;   // arrows only scrub while the panel is actually open
        if (!currentJumpToFrame) return;
        if (e.key === 'ArrowLeft' || e.key === 'ArrowDown') {
          e.preventDefault();
          currentJumpToFrame(preservedTimeTravel.cursor - 1);
        } else if (e.key === 'ArrowRight' || e.key === 'ArrowUp') {
          e.preventDefault();
          currentJumpToFrame(preservedTimeTravel.cursor + 1);
        }
      });
    }
  }

  // currentDispatch is set by App.serve before render. Click handlers use it
  // to dispatch a Msg into the running update loop.
  let currentDispatch = null;

  // A message an effect produces belongs to whoever issued the effect: the page
  // whose init or update returned it, or the shared store whose update did. It
  // used to go to whatever page was on screen when it landed, so a result that
  // arrived after a navigation went to the next page (whose update could take
  // it as its own) and the page that asked for it waited forever.
  //
  // effectIssuer is the issuer of the effect being run right now. Effects run
  // synchronously (Cmd.batch, Task.map and andThen all call .run() in place),
  // so it is visible to every async effect the moment it starts, and each one
  // copies it before its result can come back.
  //
  // Whether the issuer is covered is decided once, when its effects start to
  // run, as the phone does: `Cmd.batch [ Nav.push b, Sound.play s ]` from the
  // page on screen plays the sound, though the push already ran.
  let effectIssuer = null;
  let effectCovered = false;
  function runEffectAs(issuer, eff) {
    const prev = effectIssuer;
    const prevCovered = effectCovered;
    effectIssuer = issuer;
    effectCovered = issuerIsCovered(issuer);
    try { runEffect(eff); } finally { effectIssuer = prev; effectCovered = prevCovered; }
  }
  // Set by mountPages: delivers to a page's stack entry, on screen or covered.
  let pageDeliverHook = null;
  // An effect with no issuer (a test hook running a Cmd directly) still goes
  // to the page on screen.
  function deliverFrom(issuer, msg) {
    if (issuer && issuer.shared) { sharedDispatch(issuer.shared, msg); return; }
    if (issuer && issuer.key && pageDeliverHook) { pageDeliverHook(issuer, msg); return; }
    if (currentDispatch) currentDispatch(msg);
  }
  // Whether an issuer is a page that is not on screen (set by mountPages).
  // Such a page's model is kept up to date, but it does not move the person
  // off the page they are looking at (no navigation) or play sound behind it.
  let issuerIsCovered = () => false;
  function refusedFromCovered(what) {
    if (!effectCovered) return false;
    if (__MAR_DEV__ && global.__marDevMode && typeof console !== 'undefined' && console.warn) {
      console.warn('[mar] ignored ' + what + ' from a page that is not on screen');
    }
    return true;
  }
  // The history entries on the stack: entryId -> { depth, key }. An entry
  // that leaves the stack is retired for good: browser Forward back onto it
  // starts the page over, as a pushed page does (ADR 0009). Module level so a
  // hot reload (which re-runs mountPages) keeps it, like preservedScreenModels.
  const liveEntries = new Map();
  const retiredEntries = new Set();
  // A retired id met again (Forward onto it, or onto the UI.sheet entry that
  // shares it) gets one new id, the same for every entry that carried it, so
  // a page and its sheet entry stay one screen: retired id -> its new id.
  const restampedEntries = new Map();
  // A failure in the update of a page that is not on screen is held until that
  // page is shown again, and shown then: over the page that failed, not over
  // the one the person is looking at.
  const pendingPageErrors = new Map(); // page key -> error

  // ---- Shared: the client state that outlives navigation -------------------
  //
  // Page models live on the nav stack and die on forward navigation (ADR
  // 0009). A shared store is the designated survivor: one model per
  // `App.shared` def, alive from the first page that names it until the tab
  // closes.
  //
  // Keyed by the def VALUE, not by a name. `def` is a top-level binding, so
  // it evaluates once per module load and every use site: the page that
  // reads it, the page that writes to it, the Main that never mentions it:
  // holds the same object. That identity is the whole registration story:
  // there is no `shared` field on the App config to keep in sync, and two
  // different defs are simply two stores rather than a conflict to report.
  const sharedStores = new Map();

  // Set by mountPages. A shared model change has to repaint the current page
  // (its builder is re-applied with the new value), and only mountPages knows
  // how to paint.
  let sharedRerender = null;

  // Deferred until the first render, so `init`'s Cmd: nearly always the
  // Service.call that fills the store: dispatches into a live loop rather
  // than into a null one.
  let pendingSharedCmds = [];

  function sharedStoreFor(def) {
    let store = sharedStores.get(def);
    if (store) return store;
    // Preserved across `mar dev`'s hot reload for the same reason page models
    // are: a save should not sign you out or refetch your profile. The key is
    // the def's declaration site, which survives the reload that the def
    // OBJECT does not.
    const preserved = preservedSharedModels[def.__originKey];
    const initTuple = def.args[0];
    store = {
      def,
      model: preserved !== undefined ? preserved : initTuple.xs[0],
      update: def.args[1],
      subscriptions: def.args[2],
    };
    sharedStores.set(def, store);
    if (preserved === undefined) {
      preservedSharedModels[def.__originKey] = store.model;
      pendingSharedCmds.push({ def, cmd: initTuple.xs[1] });
    }
    return store;
  }

  function sharedDispatch(def, msg) {
    const store = sharedStoreFor(def);
    let out;
    try {
      out = unwrapModelTuple(apply(apply(store.update, msg), store.model));
    } catch (err) {
      // Unlike a page msg, a shared msg has no "arrived after navigation"
      // excuse: the store is page-independent and always live, so a failure
      // here is a real one and is reported rather than swallowed.
      if (typeof console !== 'undefined') console.error('[mar] shared update failed:', err);
      return;
    }
    store.model = out.model;
    preservedSharedModels[def.__originKey] = out.model;
    runEffectAs({ shared: def }, out.effect);
    // Repaint: pages read shared through their builder, so a change to the
    // model is a change to the view even though no page model moved.
    if (sharedRerender) sharedRerender();
  }

  // Wrap a shared subscription's tagger so its messages reach the shared
  // update instead of the current page's. Subs are merged by KEY across
  // owners (a shared `Time.every 1000` and a page's own are one timer with
  // two taggers), so the destination cannot ride on the record: it has to
  // ride on each tagger.
  function sharedSubItems(store) {
    const sub = store.subscriptions ? apply(store.subscriptions, store.model) : null;
    if (!sub || !sub.items) return [];
    return sub.items.map((it) => {
      if (!it.tagger) return it;
      const routed = native(1, ([v]) => apply(it.tagger, v));
      routed.__sharedDef = store.def;
      return Object.assign({}, it, { tagger: routed, owner: store.def.__originKey });
    });
  }

  // Deliver one subscription message to whoever owns the tagger. Every sub
  // source funnels through here so shared and page subs stay indistinguishable
  // to the reconciler.
  function deliverSub(tg, arg) {
    const msg = apply(tg, arg);
    if (tg && tg.__sharedDef) { sharedDispatch(tg.__sharedDef, msg); return; }
    if (currentDispatch) currentDispatch(msg);
  }

  // True while the rAF tick source is replaying the intermediate ticks of a
  // catch-up burst (ADR-0003: a painted frame that carries more than one tick
  // interval of real time delivers 2..4 back-to-back ticks so the GAME clock
  // keeps true speed while only the paint rate drops). Dispatch skips
  // render() for these: the burst's final tick fires with the flag off and
  // paints once for the whole frame, so a burst costs N updates + ONE view.
  let tickBurst = false;
  // Update time spent in suppressed burst ticks, folded into the next painted
  // dispatch's perfRecord so the ?perf readout keeps meaning ms per PAINTED
  // frame (not ms per dispatch, which would undercount burst frames).
  let tickBurstPerfCarry = 0;

  // ---------- Frame-time instrument (dev perf probe) ----------
  // An on-canvas FPS meter reads the TICK rate, which is quantized to the
  // display's vsync harmonics (60 / 30 / 20 on a 60 Hz panel): a frame that
  // runs a hair over the 16.7 ms budget misses vsync and reads a flat 30,
  // hiding whether it's at 18 ms (almost there) or 33 ms (far off). This
  // measures the TRUE main-thread work per dispatch (update + view + DOM
  // reconcile) with performance.now, NOT quantized, so you can watch the
  // real cost approach and cross the 16.7 ms line as you optimize.
  //
  // Off by default (zero cost). Enable with `?perf` in the URL (works on a
  // phone with no console) or `localStorage.marPerf = 1`. Once enabled,
  // `window.__marFrameMs` holds the live smoothed ms/frame and
  // `window.__marFps` the actual painted-frame count of the last second.
  const __PERF_ON = (function () {
    try {
      return /(?:^|[?&])perf(?:=[^&]*)?(?:&|$)/.test(location.search) ||
             !!localStorage.getItem('marPerf');
    } catch (e) { return false; }
  })();
  let __perfEma = 0, __perfEl = null, __perfLastPaint = 0, __perfPaints = [];
  function perfRecord(ms) {
    // Exponential smoothing so one heavy frame doesn't make the number jump.
    __perfEma = __perfEma ? __perfEma * 0.9 + ms * 0.1 : ms;
    try { global.__marFrameMs = __perfEma; } catch (e) {}
    // Repaint the readout ~4x/s, not every frame: a DOM write every frame would
    // itself cost time and jitter the very number we're trying to measure.
    const now = (typeof performance !== 'undefined') ? performance.now() : 0;
    // Actual PAINTED-frame rate. perfRecord runs exactly once per painted
    // frame: a catch-up burst's intermediate ticks fold their time in
    // WITHOUT a paint (ADR-0003), so paints in the last second ARE the
    // rendered fps. This is the true refresh the eye sees, not the
    // vsync-quantized number an on-canvas counter reads; the two now differ
    // (a Low-Power-Mode display caps painting at 30 fps while the ms/frame
    // stays a healthy 8), and the readout shows both.
    if (now) {
      __perfPaints.push(now);
      while (__perfPaints.length && now - __perfPaints[0] > 1000) __perfPaints.shift();
      try { global.__marFps = __perfPaints.length; } catch (e) {}
    }
    if (now - __perfLastPaint < 250) return;
    __perfLastPaint = now;
    if (typeof document === 'undefined' || !document.body) return;
    if (!__perfEl) {
      __perfEl = document.createElement('div');
      __perfEl.setAttribute('aria-hidden', 'true');
      __perfEl.style.cssText =
        'position:fixed;top:0;left:0;z-index:2147483647;pointer-events:none;' +
        'font:700 12px/1.35 ui-monospace,SFMono-Regular,Menlo,monospace;' +
        'padding:3px 7px;background:rgba(0,0,0,.72);white-space:pre;' +
        'border-bottom-right-radius:5px;';
      document.body.appendChild(__perfEl);
    }
    const v = __perfEma;
    const fps = __perfPaints.length;
    // Colour tracks the WORK headroom (ms vs the 16.7 ms budget), NOT the
    // paint rate: a display-capped 30 fps with cheap 8 ms frames is healthy
    // (green), not a warning: the fps number tells that story on its own.
    __perfEl.style.color = v <= 16.7 ? '#77e88a' : (v <= 33.3 ? '#f2c94c' : '#ff6b6b');
    __perfEl.textContent = fps + ' fps   ' + v.toFixed(1) + ' ms/frame';
  }

  // ---------- Subscriptions (Sub) reconciler ----------
  // A Sub is a declarative value `{k:'SUB', items:[{src,key,intervalMs,tagger}]}`
  // produced by a page's `subscriptions : Model -> Sub Msg`. After every render
  // we reconcile the live sources against the page's desired set: start newly
  // returned ones, stop ones no longer returned, refresh taggers on survivors.
  // Identity is the structural key (the data args, never the tagger), so two
  // `Time.every` at the same interval share one timer. `activeSubs` is module
  // level so a hot reload (which re-runs mountPages) can tear down the previous
  // mount's live timers via teardownAllSubs().
  //
  // Each tagger travels with its OWNER, the subscriber it belongs to: the page
  // on screen, or a shared store. A source with a current state (Device,
  // Keyboard, Gamepad) hands that state to every subscriber that starts
  // listening, and because subscribers merge by key, starting to listen is not
  // the same as the source starting: a page reached from another page that
  // already holds Device.watch joins a source that is running.
  const activeSubs = new Map(); // key -> { key, src, handle, taggers: [fn], owners: [string] }
  // A page subscribes anew each time it is shown: navigation to it (push,
  // replace, Back) or a fresh model in place. Only the page on screen listens,
  // so the web's owner for a page is one showing of it, numbered here. A shared
  // store subscribes once, for its life, under its own key.
  let subPageKey = null;
  let subPageShowing = 0;
  function pageSubOwner(key) {
    if (key !== subPageKey) { subPageKey = key; subPageShowing++; }
    return 'page:' + subPageShowing;
  }
  function isPageOwner(owner) {
    return typeof owner === 'string' && owner.startsWith('page:');
  }
  // What each page was last handed by each state source, by page key and
  // source key, while it was on screen. A page shown again with the model it
  // had (Back, a sheet closing) is handed the state only if it changed while
  // the page was covered: as if it had kept listening, so a page that acts on
  // the state is not made to act again for nothing (one that navigates on the
  // device's width would otherwise be sent forward again by every Back). A
  // fresh model has no entry and is always handed it: resetKey and
  // resetTimeTravel drop the page's entries, and teardownAllSubs drops them
  // all.
  const pageStateSeen = new Map(); // pageKey + '\u0000' + sourceKey -> value
  function forgetPageStates(pageKey) {
    const prefix = pageKey + '\u0000';
    for (const k of [...pageStateSeen.keys()]) if (k.startsWith(prefix)) pageStateSeen.delete(k);
  }
  // Every delivery of a state source goes through here: a change to every
  // subscriber, or a seed to the ones that joined.
  function deliverState(rec, taggers, owners, value) {
    if (owners.some(isPageOwner)) pageStateSeen.set(subPageKey + '\u0000' + rec.key, value);
    for (const tg of taggers) deliverSub(tg, value);
  }
  // ---- Keyboard subs (Keyboard.watch) ----
  // event.code values that scroll / activate the page: preventDefault these so
  // a game holding arrows or space doesn't also scroll. Everything else passes.
  const KEY_PREVENT_DEFAULT = new Set(['Space', 'ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight']);
  function makeKeyListener(type, onCode) {
    const handler = (e) => {
      // Don't hijack typing: ignore when a form field / contenteditable is focused.
      const tag = (e.target && e.target.tagName) || '';
      if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || (e.target && e.target.isContentEditable)) return;
      if (KEY_PREVENT_DEFAULT.has(e.code)) e.preventDefault();
      onCode(e.code);
    };
    if (typeof window !== 'undefined') window.addEventListener(type, handler);
    return handler;
  }
  function removeKeyListener(type, handler) {
    if (handler && typeof window !== 'undefined') window.removeEventListener(type, handler);
  }
  // ---- Held-key tracking + focus-loss flush ----
  // Keyboard MIRROR: the runtime owns the held-key set; Keyboard.watch delivers
  // the whole set as a record ({ down }) on every change. OS auto-repeat re-fires
  // keydown for a held key, but the set is unchanged, so nothing dispatches. The
  // OS stops delivering keyup once the window loses focus, so on blur / tab-hide
  // we clear the set (a key held at blur would otherwise stay "down" forever).
  // Fixes stuck keys across every canvas game, centrally, with no per-app code.
  const heldKeyCodes = new Set();
  const kbWatchFires = new Set();     // one fire() per active Keyboard.watch sub
  let keyBlurInstalled = false;
  function fireKbWatch() { for (const f of kbWatchFires) f(); }
  function keyboardStateRecord() {
    return VRecord({ down: VList(Array.from(heldKeyCodes).map((c) => VCtor(c))) }, ['down']);
  }
  function installKeyBlurFlush() {
    if (keyBlurInstalled || typeof window === 'undefined') return;
    keyBlurInstalled = true;
    const clear = () => { if (heldKeyCodes.size) { heldKeyCodes.clear(); fireKbWatch(); } };
    window.addEventListener('blur', clear);
    if (typeof document !== 'undefined')
      document.addEventListener('visibilitychange', () => { if (document.hidden) clear(); });
  }
  // Shared, installed once on first Keyboard.watch subscribe: OS auto-repeat
  // re-fires keydown for a held code, so guard on set membership: only a real
  // add/remove notifies watchers.
  let kbListenersInstalled = false;
  function installKbListeners() {
    if (kbListenersInstalled) return;
    kbListenersInstalled = true;
    makeKeyListener('keydown', (code) => { if (!heldKeyCodes.has(code)) { heldKeyCodes.add(code); fireKbWatch(); } });
    makeKeyListener('keyup', (code) => { if (heldKeyCodes.delete(code)) fireKbWatch(); });
    installKeyBlurFlush();
  }
  // ---- Gamepad MIRROR (Gamepad.watch) ----
  // The Gamepad API fires no events, so poll each animation frame and keep the
  // full pad state: connection, both analog sticks (deadzoned, -100..100), and
  // the held-button SET. When anything changes, notify every Gamepad.watch sub,
  // which delivers the whole pad as one record snapshot. A single rAF loop is
  // shared, started on first subscribe. Standard button index → Button ctor
  // name (W3C "standard" mapping).
  const PAD_BTN = ['A', 'B', 'X', 'Y', 'L1', 'R1', 'L2', 'R2', 'Select', 'Start', 'L3', 'R3', 'Up', 'Down', 'Left', 'Right'];
  let padRAF = 0;
  let padConnected = false;
  const heldPadButtons = new Set();
  let padLX = 0, padLY = 0, padRX = 0, padRY = 0;
  const padWatchFires = new Set();    // one fire() per active Gamepad.watch sub
  function fireGpWatch() { for (const f of padWatchFires) f(); }
  function gamepadStateRecord() {
    return VRecord({
      connected: VBool(padConnected),
      leftX: VInt(padLX), leftY: VInt(padLY),
      rightX: VInt(padRX), rightY: VInt(padRY),
      down: VList(Array.from(heldPadButtons).map((n) => VCtor(n))),
    }, ['connected', 'leftX', 'leftY', 'rightX', 'rightY', 'down']);
  }
  function padAxis(ax, i) { const raw = ax.length > i ? ax[i] : 0; return Math.abs(raw) < 0.25 ? 0 : Math.round(raw * 100); }
  function padPoll() {
    const pads = (typeof navigator !== 'undefined' && navigator.getGamepads) ? navigator.getGamepads() : [];
    let pad = null;
    for (const p of pads) { if (p) { pad = p; break; } }
    let changed = false;
    const nowConnected = !!pad;
    if (nowConnected !== padConnected) { padConnected = nowConnected; changed = true; }
    if (pad) {
      const btns = pad.buttons || [];
      for (let i = 0; i < PAD_BTN.length; i++) {
        const name = PAD_BTN[i];
        const pressed = !!(btns[i] && btns[i].pressed);
        const had = heldPadButtons.has(name);
        if (pressed && !had) { heldPadButtons.add(name); changed = true; }
        else if (!pressed && had) { heldPadButtons.delete(name); changed = true; }
      }
      const ax = pad.axes || [];
      const nlx = padAxis(ax, 0), nly = padAxis(ax, 1), nrx = padAxis(ax, 2), nry = padAxis(ax, 3);
      if (nlx !== padLX || nly !== padLY || nrx !== padRX || nry !== padRY) {
        padLX = nlx; padLY = nly; padRX = nrx; padRY = nry; changed = true;
      }
    } else if (heldPadButtons.size || padLX || padLY || padRX || padRY) {
      // pad went away: clear so held buttons / sticks self-heal.
      heldPadButtons.clear(); padLX = padLY = padRX = padRY = 0; changed = true;
    }
    if (changed) fireGpWatch();
    padRAF = requestAnimationFrame(padPoll);
  }
  function padWatchAdd(fire) {
    padWatchFires.add(fire);
    if (!padRAF && typeof requestAnimationFrame !== 'undefined') padRAF = requestAnimationFrame(padPoll);
  }
  function padWatchRemove(fire) {
    padWatchFires.delete(fire);
    if (padWatchFires.size === 0 && padRAF) { cancelAnimationFrame(padRAF); padRAF = 0; }
  }
  // ---- Sound (docs/proposals/sound.md): chip-audio SFX + loops + beds ----
  // A Sound value is { k:'SND', voices:[{ wave, freq, ms, endFreq, volume,
  // delayMs }] }. Sound.play (a Cmd) fires one; Sound.loop / Sound.voice (Subs)
  // repeat one / hold one while subscribed.
  // The envelope is the VOICE's (Sound.attack / Sound.release), never the
  // player's. It used to be neither: the one-shot renderer shaped every note
  // 8ms in / 250ms out, and the held bed 400ms in / ~90ms out, so the same Sound
  // spoke differently depending on which Sub happened to play it and no Mar
  // value could say which it wanted. See docs/proposals/sound-envelope.md.
  //
  // What stays here is NOT a house style: it is the floor below which a gain
  // change is a step discontinuity, i.e. an audible click. A voice that asks for
  // nothing gets this and only this: the shortest ramp that is not a click.
  // Every shape beyond it (a note that rings out, a crowd that arrives) is taste,
  // and taste is written in the app. ADR-0006.
  const SOUND_MIN_RAMP_MS = 5;
  const rampSec = (ms) => Math.max(SOUND_MIN_RAMP_MS, ms == null ? 0 : ms) / 1000;
  const voiceAttackSec = (v) => rampSec(v.attack);
  const voiceReleaseSec = (v) => rampSec(v.release);
  // The decay stage. Unlike attack and release it has an OFF value: 0 means the
  // voice never asked for one, and then the envelope is exactly what it was
  // before this existed (attack, hold at full, release). So no sound in the repo
  // moved when decay landed. `rampSec`'s click floor only applies once a decay
  // has actually been asked for.
  const voiceDecaySec = (v) => (v.decay > 0 ? rampSec(v.decay) : 0);
  // The level a decay falls TO, as a fraction of the voice's own peak. Floored at
  // the same 0.0002 `pk` uses, because an exponential ramp cannot reach zero:
  // `Sound.decay 260 0` means "fall to silence", and silence here is the floor.
  const voiceSustain = (v, pk) =>
    Math.max(0.0002, pk * Math.max(0, Math.min(100, v.sustain == null ? 100 : v.sustain)) / 100);
  // The level a HELD source settles at. Same sustain level, but only when a decay
  // was asked for — without one the voice holds at its peak, as it always did.
  const heldLevel = (v, pk) => (v.decay > 0 ? voiceSustain(v, pk) : Math.max(0.0002, pk));
  let audioCtx = null, masterGain = null, noiseBuf = null, noiseBuf2 = null;
  // The bus sums voices LINEARLY, and nothing downstream was catching the result:
  // an app that sounds several things at once could ask for more than full scale
  // and the output was hard-clipped by the device: heard as a chord that is not
  // just louder but broken. Measured on examples/pocket-synth: three held organ
  // notes reached 1.05 to 1.29 depending on where their phases landed.
  //
  // The fix is a stateless soft ceiling, not a compressor. A compressor would duck
  // the WHOLE mix whenever one sound got loud, which is a behaviour no app can see
  // coming or reason about; this is a function of the sample and nothing else, so
  // it stays as predictable as the rest of the engine.
  //
  // Below the knee it is EXACTLY the identity, so every app that was already
  // within range sounds as before. Above it the curve bends and asymptotes to 1,
  // so full scale can be approached and never passed. Same category as
  // SOUND_MIN_RAMP_MS: not house style, a floor (here a ceiling) past which the
  // output stops being a faithful rendering of what was asked for.
  // A WaveShaper maps its INPUT RANGE -1..+1 across the whole table, and anything
  // past that CLAMPS to the end entry. So a table built plainly over -1..+1 stops
  // being a ceiling exactly where a ceiling starts to matter: every peak above 1
  // comes out at the same 0.9285, flat. That is hard limiting, the thing this
  // exists to avoid, and it is also a divergence — iOS computes the tanh directly
  // and keeps bending, so the same overdriven chord measured up to 0.64 dB apart
  // on the two runtimes, with a different SHAPE on each.
  //
  // The fix is to cover a wider input range and pay for it at the input: the
  // table spans ±CEILING_SPAN and a fixed gain of 1/CEILING_SPAN sits in front of
  // it, so table index still means "input", just over a wider domain. An earlier
  // draft widened the span WITHOUT that gain, which is why the comment used to
  // warn against it: on its own it doubles every quiet signal and saturates the
  // rest. The gain is the missing half, not a workaround for it.
  //
  // The table grows with the span so the resolution below full scale is exactly
  // what it was, and 2 is enough: ceiling(2) is 0.99990, within 0.001 dB of the
  // asymptote, so clamping past there costs nothing anyone can hear.
  const CEILING_KNEE = 0.7;
  const CEILING_SPAN = 2;
  const ceilingAt = (x) => {
    const a = Math.abs(x), k = CEILING_KNEE;
    const y = a <= k ? a : k + (1 - k) * Math.tanh((a - k) / (1 - k));
    return x < 0 ? -y : y;
  };
  function soundCeilingCurve() {
    const n = 4096 * CEILING_SPAN, c = new Float32Array(n);
    for (let i = 0; i < n; i++) c[i] = ceilingAt(((i / (n - 1)) * 2 - 1) * CEILING_SPAN);
    return c;
  }
  // Exposed so the ceiling's promises can be tested as pure arithmetic, without
  // an AudioContext: see TestMasterCeilingIsSoftAndTransparent. `__marSoundCeilingAt`
  // answers what the shaper does to a given INPUT — table lookup, pre-gain and
  // clamping included — so a test asks the runtime rather than re-deriving the
  // index arithmetic and agreeing with itself.
  if (typeof globalThis !== 'undefined') {
    globalThis.__marSoundCeilingCurve = soundCeilingCurve;
    globalThis.__marSoundCeilingAt = (x) => {
      const c = soundCeilingCurve(), n = c.length;
      const scaled = Math.max(-1, Math.min(1, x / CEILING_SPAN));
      return c[Math.max(0, Math.min(n - 1, Math.round(((scaled + 1) / 2) * (n - 1))))];
    };
  }
  // Returns the HEAD of the ceiling chain: whatever connects here is scaled into
  // the shaper's domain first.
  function soundCeiling(ctx) {
    const ws = ctx.createWaveShaper();
    ws.curve = soundCeilingCurve();
    ws.oversample = '2x';                          // the bend makes harmonics; do not alias them
    ws.connect(ctx.destination);
    const pre = ctx.createGain();
    pre.gain.value = 1 / CEILING_SPAN;
    pre.connect(ws);
    return pre;
  }
  // Sound.Wave -> oscillator, in ONE place. The one-shot path and the held path
  // each used to carry their own copy of this mapping, written as a ternary with
  // a `square` fallback — which is a trap, not a default: a new member of
  // Sound.Wave rendered as a SQUARE rather than failing, silently, in two files
  // that had to be found separately. Sine walked into exactly that. One table
  // now, and an unknown tag is a thrown error instead of a wrong timbre.
  const OSC_TYPE = { Square: 'square', Triangle: 'triangle', Sawtooth: 'sawtooth', Sine: 'sine' };
  // Duty is a pulse WIDTH: only the square has a pulse to widen. The other three
  // shapes ignore it, which is why this is a predicate and not `wave === Square`
  // — the tag can also be the internal 'Rest', handled before we get here.
  const dutyApplies = (v) => !(v.wave === 'Triangle' || v.wave === 'Sawtooth' || v.wave === 'Sine');
  // Vibrato is a wobble in PITCH, so it needs a pitch to wobble. Every wave has
  // one except noise, where freq 0 is the way an app says "no pitch asked for".
  const vibratoApplies = (v) => v.wave !== 'Noise' || v.freq > 0;
  const setOscWave = (ctx, node, v) => {
    if (dutyApplies(v) && v.duty && v.duty !== 50) {
      node.setPeriodicWave(dutyWave(ctx, v.duty));
    } else {
      const t = OSC_TYPE[v.wave];
      if (!t) throw new Error('Sound: unknown wave ' + v.wave);
      node.type = t;
    }
    // Sound.detune: a fixed offset in cents. `detune` is an AudioParam and
    // vibrato CONNECTS its LFO into the same one; a connected input sums with the
    // intrinsic value, so the static offset and the wobble compose on their own
    // with nothing to reconcile.
    if (v.detune) node.detune.value = v.detune;
  };
  let dutyWaveCache = {};   // Sound.duty pulse-width -> band-limited PeriodicWave
  let soundMuted = false;
  let soundMasterLevel = 0.35;   // Sound.master 0..100 -> gain; 0.35 is the default headroom (~master 70)
  // Mute is APP state, not engine state. This used to boot from a `marMuted`
  // key in localStorage that nothing in the framework ever wrote, so it was half
  // a feature: an app calling Sound.setMuted did not persist it, a stale value
  // left by anything else on the origin silently booted the app muted, and the
  // model had no way to see or override either. Mar has no storage API, so the
  // key could not even be written from Mar code. An app that wants mute to
  // persist owns that decision and calls Sound.setMuted itself.
  function ensureAudio() {
    if (typeof window === 'undefined') return null;
    if (!audioCtx) {
      const AC = window.AudioContext || window.webkitAudioContext;
      if (!AC) return null;
      audioCtx = new AC();
      masterGain = audioCtx.createGain();
      masterGain.gain.value = soundMuted ? 0 : soundMasterLevel;   // headroom so stacked voices don't clip
      masterGain.connect(soundCeiling(audioCtx));
    }
    if (audioCtx.state === 'suspended') { try { audioCtx.resume(); } catch (e) {} }
    return audioCtx;
  }
  // Ramp the master gain to the live target (0 when muted). Sound.setMuted /
  // Sound.master + the dev mute hook call this so the app can duck EVERYTHING
  // already sounding (beds, loops), not just gate new plays.
  function applyMaster() {
    if (!masterGain || !audioCtx) return;
    const t = audioCtx.currentTime;
    try { masterGain.gain.cancelScheduledValues(t); masterGain.gain.setTargetAtTime(soundMuted ? 0 : soundMasterLevel, t, 0.02); } catch (e) {}
  }
  // Dev-only: the time-travel panel silences audio while you inspect the past, a
  // frozen world shouldn't keep droning its engine bed. Opening mutes (only if
  // sound was ON); closing un-mutes ONLY the mute WE applied, so a mute the user
  // set themselves survives. Wired from the dev dock's expand/collapse below.
  let mutedByTimeTravel = false;
  function setTimeTravelMuted(open) {
    if (open) {
      if (!soundMuted) { soundMuted = true; mutedByTimeTravel = true; applyMaster(); }
    } else if (mutedByTimeTravel) {
      mutedByTimeTravel = false; soundMuted = false; applyMaster();
    }
  }
  // Browsers block audio until a user gesture, so a program that makes
  // sound opens (and resumes) its AudioContext on the first tap/key:
  // inside the gesture, where every browser allows it.
  //
  // Armed only for programs that actually reference Sound. An open
  // AudioContext wired to the destination is what makes the browser badge
  // the tab with the "playing audio" speaker, so arming this everywhere
  // put that badge on silent apps: click once anywhere in a CRUD app and
  // it claimed to be playing audio forever after. See armAudioUnlock's
  // caller in marRun.
  let audioUnlockArmed = false;
  function armAudioUnlock() {
    if (audioUnlockArmed || typeof document === 'undefined') return;
    audioUnlockArmed = true;
    const wake = () => ensureAudio();
    document.addEventListener('pointerdown', wake);
    document.addEventListener('keydown', wake);
  }
  // Does the loaded program reference the Sound module anywhere? Every
  // reference carries the module the same way whatever the node kind:
  // `module: ["Sound"]` on EQualified for Sound.play, on ECtor/PCtor for
  // Sound.Wave, on the import decl for `import Sound`. Walks the AST with
  // an early exit rather than stringifying it, so a silent app pays a
  // scan that stops at the first hit and a game pays it once at boot.
  function referencesSound(node) {
    if (node === null || typeof node !== 'object') return false;
    if (Array.isArray(node)) {
      for (let i = 0; i < node.length; i++) {
        if (referencesSound(node[i])) return true;
      }
      return false;
    }
    const m = node.module;
    if (Array.isArray(m) && m.length === 1 && m[0] === 'Sound') return true;
    for (const k in node) {
      if (k === 'module') continue;
      if (referencesSound(node[k])) return true;
    }
    return false;
  }
  // Exposed so the rule can be tested against REAL serializer output:
  // it reads a shape (`module: ["Sound"]`) that only the Go serializer
  // produces, and if that shape ever changes every game goes silent with
  // no error anywhere. See TestSilentProgramsDoNotArmAudio.
  if (typeof globalThis !== 'undefined') globalThis.__marReferencesSound = referencesSound;
  // preview/test hook: window.marSoundMute(true) silences everything.
  if (typeof window !== 'undefined') window.marSoundMute = (b) => { soundMuted = !!b; applyMaster(); };
  // A looping white-noise clip, DE-CLICKED at the loop point. Two artefacts to beat:
  //   1. Repetition: a single looped clip, however long, is heard as a recurring
  //      pattern. Cured by DECORRELATION - a held source (soundHeldStart) sums two of these
  //      at incommensurate lengths so the wash never repeats.
  //   2. The seam "estalo": when a buffer loops, buf[n-1] -> buf[0] is a FIXED
  //      sample-to-sample STEP that recurs every loop period; through the high-pass
  //      that periodic step is an audible click every few seconds. (It is NOT about
  //      the step's magnitude - white noise steps everywhere - it is the PERIODIC
  //      recurrence the ear locks onto.) Cured here by ramping the first + last few ms
  //      to zero, so buf[0] and buf[n-1] are ~0 and the seam step vanishes. The ~4ms
  //      dip once per loop is inaudible in dense noise, and the other (decorrelated)
  //      layer is mid-clip then anyway, so the summed wash never actually dips.
  function makeNoiseLoop(ctx, seconds) {
    const n = (ctx.sampleRate * seconds) | 0;
    const buf = ctx.createBuffer(1, n, ctx.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < n; i++) d[i] = Math.random() * 2 - 1;
    const fade = Math.max(1, (ctx.sampleRate * 0.004) | 0);
    for (let i = 0; i < fade; i++) {
      const w = i / fade;
      d[i] *= w;
      d[n - 1 - i] *= w;
    }
    return buf;
  }
  function soundNoiseBuffer(ctx) {
    // Primary noise clip (~6.3s); also the front is reused for one-shot noise.
    if (!noiseBuf) noiseBuf = makeNoiseLoop(ctx, 6.3);
    return noiseBuf;
  }
  function soundNoiseBuffer2(ctx) {
    // The second, different-length noise layer (detuned in soundHeldStart) - its loop
    // is incommensurate with the primary's, so the summed wash never repeats.
    if (!noiseBuf2) noiseBuf2 = makeNoiseLoop(ctx, 4.9);
    return noiseBuf2;
  }
  // The tremolo LFO. A plain OscillatorNode sine starts at phase 0, which is
  // VALUE 0: a note would begin half-faded and rise into its own attack. A
  // cosine starts at +1, so the note speaks at its written level and the wobble
  // only ever takes away from there. One harmonic, real[1] = 1: that IS a cosine.
  let cosWaveCache = null;
  function cosineWave(ctx) {
    if (!cosWaveCache) {
      const real = new Float32Array([0, 1]), imag = new Float32Array([0, 0]);
      cosWaveCache = ctx.createPeriodicWave(real, imag);
    }
    return cosWaveCache;
  }
  // Sound.tremolo, as a gain the LFO rides. Returns the node to put BETWEEN the
  // voice's shaping and its envelope, or null when the voice asked for none.
  //
  // Depth is a percentage OF the voice's level and it only attenuates: the gain
  // sits at 1 - d/2 and the LFO swings ±d/2 around it, so the peak is exactly
  // the level the voice was written at and the trough is (1 - d) of it. Depth
  // 100 therefore touches silence and nothing can come out LOUDER than it says
  // — which is what keeps the headroom argument for the master ceiling true.
  function soundTremoloGain(ctx, v, dest, t0, stopAt) {
    if (!(v.tremDepth > 0)) return null;
    const d = Math.max(0, Math.min(100, v.tremDepth)) / 100;
    const tg = ctx.createGain();
    tg.gain.value = 1 - d / 2;
    tg.connect(dest);
    const lfo = ctx.createOscillator();
    lfo.setPeriodicWave(cosineWave(ctx));
    lfo.frequency.setValueAtTime(Math.max(1, v.tremRate || 6), t0);
    const lg = ctx.createGain();
    lg.gain.value = d / 2;
    lfo.connect(lg).connect(tg.gain);
    lfo.start(t0);
    if (stopAt != null) lfo.stop(stopAt);
    return { node: tg, lfo };
  }
  // Sound.duty: a band-limited pulse of width d (0..1) as a WebAudio PeriodicWave.
  // The nth harmonic of a duty-d pulse is (2/nπ)·sin(nπd): at d=0.5 the even
  // harmonics vanish, i.e. it reduces to a plain square. 12.5% is thin/nasal, 25%
  // the classic NES lead, 50% hollow. Cached per duty so each note is cheap.
  function dutyWave(ctx, duty) {
    const d = Math.max(5, Math.min(95, duty)) / 100;
    const key = String(Math.round(d * 100));
    if (!dutyWaveCache[key]) {
      const N = 32;
      const real = new Float32Array(N + 1), imag = new Float32Array(N + 1);
      for (let n = 1; n <= N; n++) imag[n] = (2 / (n * Math.PI)) * Math.sin(n * Math.PI * d);
      dutyWaveCache[key] = ctx.createPeriodicWave(real, imag);
    }
    return dutyWaveCache[key];
  }
  // Tone shaping, from the Sound value and nowhere else: Sound.lowCut trims
  // below a frequency, Sound.highCut trims above it. Returns the node the source
  // should feed. Asking for neither creates nothing, so the ordinary path keeps
  // the exact graph it always had.
  //
  // These used to be fixed at 340-2600Hz inside the ambient bed, tuned to make
  // one game's stadium crowd sit right. That made every sustained noise in every
  // app come out crowd-shaped, with no way to ask for wind or rain instead. The
  // numbers now live in the app that wants them.
  function soundFilterChain(ctx, v, dest) {
    let head = dest;
    if (v.highCut > 0) {
      const lp = ctx.createBiquadFilter();
      lp.type = 'lowpass';
      lp.frequency.value = v.highCut;
      lp.Q.value = 0.4;
      lp.connect(head);
      head = lp;
    }
    if (v.lowCut > 0) {
      const hp = ctx.createBiquadFilter();
      hp.type = 'highpass';
      hp.frequency.value = v.lowCut;
      hp.Q.value = 0.5;
      hp.connect(head);
      head = hp;
    }
    return head;   // source -> highpass -> lowpass -> dest
  }

  // Sound.pan: where the voice sits between the ears. Wires the envelope gain to
  // `dest` through two constant gains and a 2-in merger, and at pan 0 wires the
  // single edge that has always been there instead.
  //
  //   gainL = 1 - max(0,  pan) / 100
  //   gainR = 1 - max(0, -pan) / 100
  //
  // Not a StereoPannerNode, which applies the equal-power law to a mono input and
  // therefore sits at 0.707 per channel in the CENTRE. That is the textbook law
  // and the wrong one here: it would make all ~670 existing tone and sweep calls
  // in this repo quieter the day pan shipped. This law puts centre at 1.0/1.0, so
  // the 3 dB is paid only by whoever asks to be placed hard to one side.
  //
  // Three things about the shape, each of which was a bug first:
  //
  //  - At pan 0 it builds NOTHING. That is what makes the claim "an unpanned
  //    sound is bit-identical to before" true by construction rather than by
  //    arithmetic, and it is also what keeps the fake AudioContexts in the Go
  //    tests working, since they expose no createChannelMerger.
  //  - createChannelMerger(2), and both edges name their input index. The default
  //    is SIX inputs, and a 6-channel signal reaching a 'speakers' destination is
  //    read as 5.1 and folded down with surround coefficients: the same 3 dB loss
  //    by another route, and an image that is not left/right at all. Omitting the
  //    index instead sums both gains into input 0, which pans nothing while
  //    sounding almost right.
  //  - It goes AFTER the envelope gain, never before the cuts. A BiquadFilter is
  //    channelCountMode 'max', so a stereo signal doubles the filter work for
  //    every cut in the repo, and a fan-out upstream of the cuts makes the graph
  //    walk in sound_shaping_test.go order-dependent.
  //
  // Panned and unpanned voices mix safely: nothing in this file sets channelCount
  // or channelInterpretation, so masterGain computes 2 channels as soon as one
  // panned voice connects and up-mixes the mono ones by duplication, with no
  // level change. The master ceiling is a WaveShaper and so limits per channel,
  // which is what prevents clipping per channel but does mean that at high level
  // a hard pan also nudges the perceived position: one side is past the knee
  // while the other is still linear.
  function soundPanOut(ctx, v, g, dest) {
    const pan = Math.max(-100, Math.min(100, v.pan || 0));
    if (pan === 0) { g.connect(dest); return; }
    const gl = ctx.createGain();
    const gr = ctx.createGain();
    gl.gain.value = 1 - Math.max(0, pan) / 100;
    gr.gain.value = 1 - Math.max(0, -pan) / 100;
    const merger = ctx.createChannelMerger(2);
    g.connect(gl); g.connect(gr);
    gl.connect(merger, 0, 0);
    gr.connect(merger, 0, 1);
    merger.connect(dest);
  }

  function scheduleVoice(ctx, v, at, dest) {
    // A rest (Sound.rest) occupies time in a sequence but emits nothing.
    if (v.wave === 'Rest') return;
    const t0 = at + (v.delayMs || 0) / 1000;
    // `|| 0`, not `|| 100`. A voice whose length is genuinely 0 used to fall
    // through to a 100 ms default here, which disagreed with everything around
    // it: iOS plays it for the 20 ms floor, and this file's own loop scheduler
    // measures the cycle with `v.ms || 0`, so such a voice sounded for five
    // times the span its own loop had reserved and ran into the next pass. The
    // floor below is the documented rule -- anything shorter than 20 ms is
    // played as 20 ms -- and it is the only rule there should be.
    const dur = Math.max(0.02, (v.ms || 0) / 1000);
    const peak = Math.max(0, Math.min(100, v.volume == null ? 60 : v.volume)) / 100;
    const pk = Math.max(0.0002, peak);
    const g = ctx.createGain();
    soundPanOut(ctx, v, g, dest || masterGain);
    // Attack -> sustain -> release. Short sounds (dur <= tail) decay over their whole
    // length exactly as before (punchy blips); LONG notes (the goal shout) hold at
    // full then release only over the last `tail`, so a 3-4s "goooool" stays strong
    // instead of fading away the moment it starts.
    // The envelope comes from the VOICE (Sound.attack / Sound.release), so the
    // same Sound speaks the same way here and in the held bed. Both are clamped
    // to the note's own span: a release longer than the note cannot eat its
    // attack, and an attack longer than half the note cannot swallow it whole.
    const atk = Math.min(dur / 2, Math.max(0.0005, voiceAttackSec(v)));
    const rel = Math.min(dur - atk, voiceReleaseSec(v));
    // Sound.decay: fall from the peak to a held level, in the space the attack
    // and the release leave behind. A struck thing loses energy in proportion to
    // the energy it still has, so this is exponential like the other two stages —
    // it is the shape the ear is calibrated on, and its absence is what made
    // every note read as a rectangle. `decay 0` keeps `dec` at 0 and `sus` at the
    // peak, which is byte-for-byte the old three-event envelope.
    const dec = Math.min(voiceDecaySec(v), Math.max(0, dur - atk - rel));
    const sus = dec > 0 ? voiceSustain(v, pk) : pk;
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.exponentialRampToValueAtTime(pk, t0 + atk);                // attack
    if (dec > 0) g.gain.exponentialRampToValueAtTime(sus, t0 + atk + dec);  // decay
    g.gain.setValueAtTime(sus, t0 + Math.max(atk + dec, dur - rel));  // hold at level
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + dur);           // release to silence
    let node;
    if (v.wave === 'Noise') {
      node = ctx.createBufferSource();
      node.buffer = soundNoiseBuffer(ctx);
      node.loop = true;
      // Noise used to ignore the note completely: the same flat hiss on every
      // key, the one wave you could not actually PLAY. Resampling the clip by
      // the note pitches it: low keys stretch it into a rumble, high keys
      // squeeze it into a hiss, so the same oscillator covers thunder, surf,
      // engine and cymbal depending on where you play it. Ratio is against A4,
      // the tuning reference, clamped so an extreme note cannot ask for an
      // absurd rate. (The clip itself stays untouched: soundNoiseBuffer is
      // shared with the ambient bed, which must keep its own wash.)
      const nf = Math.max(1, v.freq || 440);
      const noiseRate = (f) => Math.max(0.05, Math.min(8, Math.max(1, f) / 440));
      node.playbackRate.setValueAtTime(noiseRate(nf), t0);
      // Everything below is guarded on a REAL pitch having been asked for.
      // freq 0 is how every game in the repo writes noise, and it means "no
      // pitch": those keep the flat clip they always had, so every pitch word
      // stays inert for them.
      //
      // `detune` is INSIDE that guard, and it did not use to be. An unpitched
      // clip has no pitch to shift, and iOS agrees — there freq 0 selects
      // full-band white noise, which no cents offset can move. On the web the
      // same call resampled the clip, so `Sound.detune 600` on the noise every
      // game in this repo writes came out as a different timbre in the browser
      // and as nothing at all on the phone. Nobody had written it yet: all 1166
      // noise voices in the repo are unpitched and none is detuned.
      if (v.freq > 0) {
        // Sound.detune on a pitched clip. An AudioBufferSourceNode has a
        // `detune` param too, and the spec folds it into the rate as
        // playbackRate * 2^(cents/1200) — which is what detuning a CLIP means.
        // iOS reaches the same number from the other side, by scaling the
        // sample-and-hold frequency, so a detuned noise agrees across the two
        // without either doing the arithmetic twice.
        if (v.detune) node.detune.value = v.detune;
        // Sound.sweep on noise. The oscillator branch ramps `frequency`; here
        // the same glide is a ramp of the resample rate, which is what pitch
        // MEANS for a noise clip. A falling one is the classic explosion.
        if (v.endFreq && v.endFreq !== v.freq) {
          const hold = Math.min(dur, Math.max(0, (v.holdMs || 0) / 1000));
          if (hold > 0) node.playbackRate.setValueAtTime(noiseRate(nf), t0 + hold);
          node.playbackRate.linearRampToValueAtTime(noiseRate(v.endFreq), t0 + dur);
        }
      }
    } else {
      node = ctx.createOscillator();
      setOscWave(ctx, node, v);   // wave + duty + detune, shared with the held path
      const f0 = Math.max(1, v.freq || 440);
      if (v.arp && v.arp.length) {
        // Sound.arp: step the pitch fast through [base, ...arp] on ONE oscillator
        // (the classic chiptune "chord" from a single channel). ~50 Hz = one step
        // per frame. Mutually exclusive with sweep/hold.
        // The offsets are SEMITONES from this voice's own pitch, so the step
        // list is derived from f0 rather than read as frequencies. That is what
        // the reference has always said and what makes an arpeggio survive
        // Sound.transpose: the whole figure moves with the note it belongs to.
        const seq = [f0].concat(v.arp.map(x => Math.max(1, Math.round(f0 * Math.pow(2, x / 12)))));
        // Each step time is computed from its INDEX, not accumulated. Adding
        // 0.02 repeatedly walks off the grid -- the sixth step lands at
        // 0.12000000000000001 -- so a boundary falls a hair later here than it
        // does on iOS, where the step is `Int(ts / 0.02)` from the note's own
        // clock. One step of an arpeggio either way is 20 ms of a different
        // note, and the playback conformance harness reads it as the two
        // runtimes disagreeing, which they now do not.
        for (let i = 0; ; i++) {
          const t = t0 + i * 0.02;
          if (t >= t0 + dur) break;
          node.frequency.setValueAtTime(seq[i % seq.length], t);
        }
      } else {
        node.frequency.setValueAtTime(f0, t0);
        if (v.endFreq && v.endFreq !== v.freq) {
          // Optional hold: keep the pitch flat at f0 for the first `holdMs`, THEN
          // glide to endFreq over the remaining time - one oscillator, one envelope.
          const hold = Math.min(dur, Math.max(0, (v.holdMs || 0) / 1000));
          if (hold > 0) node.frequency.setValueAtTime(f0, t0 + hold);
          node.frequency.linearRampToValueAtTime(Math.max(1, v.endFreq), t0 + dur);
        }
      }
    }
    // Sound.vibrato: a sine LFO on `detune` (cents), so the wobble is musical
    // and independent of pitch. depth = cents, rate = Hz.
    //
    // ONE block for both branches, because both nodes expose the same `detune`
    // param in the same unit. The noise branch used to carry its own copy that
    // modulated `playbackRate` by `rate * (2^(depth/1200) - 1)` — a linear
    // approximation of the ratio, and only an approximation: it is symmetric in
    // RATE where this is symmetric in CENTS, so a 60-cent wobble came out
    // +60/-62 there against +60/-60 here and on iOS, and a 1200-cent one asked
    // for rate 0 on the way down. Unreachable today (no app plays pitched
    // noise) and wrong on both counts, so it is gone rather than fixed.
    if (vibratoApplies(v) && v.vibDepth > 0) {
      const lfo = ctx.createOscillator();
      lfo.frequency.setValueAtTime(Math.max(1, v.vibRate || 6), t0);
      const lg = ctx.createGain();
      lg.gain.setValueAtTime(v.vibDepth, t0);
      lfo.connect(lg).connect(node.detune);
      lfo.start(t0);
      lfo.stop(t0 + dur + 0.03);
    }
    // Sound.tremolo sits AFTER the tone shaping and before the envelope, where
    // a tremolo pedal sits: amplitude modulation makes sidebands, and a filter
    // in front of it would be shaping the note rather than the wobble.
    const trem = soundTremoloGain(ctx, v, g, t0, t0 + dur + 0.03);
    node.connect(soundFilterChain(ctx, v, trem ? trem.node : g));
    // A noise source starts PAST the clip's fade-in. makeNoiseLoop tapers the
    // first and last 4 ms of the buffer so the seam is inaudible when it wraps,
    // which a held bed needs and a one-shot does not: starting at offset 0 put
    // that ramp on the front of every noise hit in every app, so a hit began at
    // exactly zero and took 4 ms to arrive. On a 55 ms hit that is the first 7%
    // of the note, up to 4.8 dB down, and iOS has no such taper -- the same
    // explosion had a softer edge in the browser than on the phone. The offset
    // costs nothing: one point of a 6.3 second clip of noise is as good as
    // another.
    if (v.wave === 'Noise') node.start(t0, 0.01); else node.start(t0);
    node.stop(t0 + dur + 0.03);
  }
  // This one DOES skip while muted, unlike the three persistent sources: a
  // one-shot has no future to be resumed into, so not creating nodes nobody can
  // hear is free and correct.
  function soundPlayNow(snd) {
    const ctx = ensureAudio();
    if (!ctx || soundMuted || !snd || !snd.voices) return;
    const at = ctx.currentTime + 0.02;
    for (const v of snd.voices) scheduleVoice(ctx, v, at);
  }
  // Exposed for tests. scheduleVoice is the unit: hand it a voice, a start time
  // and a destination of your own and it builds exactly what the browser would
  // be given, which is what the playback conformance driver reads back. See
  // internal/conformance/sound_playback.go and TestZeroLengthNoteIsTheDocumentedFloor.
  if (typeof globalThis !== 'undefined') globalThis.__marTestScheduleVoice = scheduleVoice;
  // The start of a HELD source, behind both Sound.voice and Sound.glide: each
  // voice becomes ONE continuous node held at a steady level (fades in on start,
  // out on stop), NOT a re-triggered grain. So an organ key stays flat with no
  // looping pulse, a wash stays constant, and swapping sources cross-fades
  // cleanly. Noise voices become TWO decorrelated looping layers so a wash never
  // audibly repeats (see below); oscillators hold their pitch.
  // (Looping MELODIES are Sound.loop / soundLoopStart below, not this.)
  //
  // The oscillator branch applies the same TIMBRE shaping as the one-shot path
  // (scheduleVoice): duty, vibrato, and the lowCut/highCut filters. It used to
  // hold a bare, unshaped tone, which made a held sound useless as a held NOTE: a
  // synth pad lost its wobble and its pulse width the moment it stopped
  // re-triggering, so holding a key had to mean looping (and audibly
  // re-attacking) just to keep the timbre. The difference from scheduleVoice is
  // LIFETIME, which is the whole point: no amplitude envelope, and the vibrato
  // LFO never stops.
  //
  // What a held source deliberately does NOT apply is the per-note PITCH ENVELOPE
  //: Sound.sweep and Sound.arp. Both describe how one note's pitch moves over
  // its own length, and a held source has no length; worse, for Sound.glide the
  // pitch is a live parameter that soundGlideTo slides from underneath (that is
  // how an engine note tracks speed), so a scheduled ramp here would be silently
  // overwritten by the next slide. Those stay with Sound.loop / Sound.play, which
  // own a note's span.
  function soundHeldStart(snd) {
    const ctx = ensureAudio();
    // Not guarded on soundMuted: see soundLoopStart. A held voice that never
    // started cannot be resumed by unmuting.
    if (!ctx || !snd || !snd.voices) return { nodes: [] };
    const at = ctx.currentTime + 0.02;
    const nodes = [];
    for (const v of snd.voices) {
      if (v.wave === 'Rest') continue;   // a bed has no use for silence voices
      const peak = Math.max(0, Math.min(100, v.volume == null ? 60 : v.volume)) / 100;
      const g = ctx.createGain();
      soundPanOut(ctx, v, g, masterGain);
      g.gain.setValueAtTime(0.0001, at);
      // Fade in over the voice's own attack. This used to be a hardcoded 400ms:
      // right for a drone that starts once a session, and half a second of lag on
      // anything started per event, like a key. The number lives in the value now.
      //
      // A held source arrives at the voice's SUSTAIN level, not its peak, which is
      // what a sustain level means for something with no end. What it cannot
      // honour is the decay TIME: this path holds one node at one level and has no
      // per-note clock to fall along, and inventing one would rebuild the note
      // renderer inside the bed — the exact drift sound-envelope.md was written to
      // stop. So the level is honoured and the time is not, stated here rather
      // than discovered: for a decay you can hear, use Sound.play / loop / once.
      g.gain.exponentialRampToValueAtTime(heldLevel(v, peak), at + Math.max(0.0005, voiceAttackSec(v)));
      let node, extra = [];
      // Sound.tremolo on a held source: the same gain the one-shot path builds,
      // with no stop time, so the wobble breathes for as long as the bed lives —
      // exactly the lifetime rule vibrato follows two branches down.
      const tremHead = (voice, at0, dst, keep) => {
        const t = soundTremoloGain(ctx, voice, dst, at0, null);
        if (!t) return dst;
        keep.push(t.lfo);   // soundHeldStop stops every extra alongside the node
        return t.node;
      };
      if (v.wave === 'Noise') {
        // TWO decorrelated noise layers summed into a trim gain, then through
        // whatever shaping the Sound value asked for. The layers are different
        // clips of different lengths, the second detuned, so their loops are
        // incommensurate and the wash NEVER repeats identically. A single looped clip
        // - at any length - is heard as a recurring pattern (the "estalo"/"batida de
        // caixa"); two incommensurate loops realign only after their LCM, i.e. never.
        // The 0.7 trim offsets the ~+3dB of summing two noise sources.
        const nmix = ctx.createGain();
        nmix.gain.value = 0.7;
        nmix.connect(soundFilterChain(ctx, v, tremHead(v, at, g, extra)));
        node = ctx.createBufferSource();
        node.buffer = soundNoiseBuffer(ctx);
        node.loop = true;
        node.connect(nmix);
        const nb = ctx.createBufferSource();
        nb.buffer = soundNoiseBuffer2(ctx);
        nb.loop = true;
        nb.playbackRate.value = 0.87;   // detune the 2nd layer so it can never lock to the 1st
        nb.connect(nmix);
        nb.start(at);
        extra.push(nb);
      } else {
        node = ctx.createOscillator();
        setOscWave(ctx, node, v);   // the same wave + duty + detune the one-shot uses
        node.frequency.setValueAtTime(Math.max(1, v.freq || 440), at);
        // Sound.vibrato: no stop time, so the wobble breathes for as long as the
        // bed lives. Driven on `detune` (cents) precisely so soundGlideTo can glide
        // `frequency` underneath a live bed without disturbing it.
        if (v.vibDepth && v.vibDepth > 0) {
          const lfo = ctx.createOscillator();
          lfo.frequency.setValueAtTime(Math.max(1, v.vibRate || 6), at);
          const lg = ctx.createGain();
          lg.gain.setValueAtTime(v.vibDepth, at);
          lfo.connect(lg).connect(node.detune);
          lfo.start(at);
          extra.push(lfo);   // soundHeldStop stops every extra alongside the node
        }
        node.connect(soundFilterChain(ctx, v, tremHead(v, at, g, extra)));
      }
      node.start(at);
      // `_peak` and `_attackEnd` exist for soundGlideTo, and they are not
      // bookkeeping: they are what keeps it from destroying this envelope.
      // Without `_peak`, its "did the level change?" guard read undefined on the
      // FIRST slide and ran anyway, cancelling the ramp scheduled just above.
      nodes.push({ node, gain: g, extra, release: voiceReleaseSec(v), _peak: peak, _attackEnd: at + Math.max(0.0005, voiceAttackSec(v)) });
    }
    return { nodes };
  }
  // Exposed for tests, like scheduleVoice above: a held source is the other
  // half of what this file plays, and the playback conformance driver reads its
  // graph the same way. See internal/conformance/sound_playback.go.
  if (typeof globalThis !== 'undefined') globalThis.__marTestHeldStart = soundHeldStart;
  function soundHeldStop(h) {
    if (!h || !h.nodes || !audioCtx) return;
    const t = audioCtx.currentTime;
    for (const rec of h.nodes) {
      try {
        // Fade out over the voice's own release, as a RAMP: the same shape and
        // the same number the one-shot path uses, so `Sound.release 250` means
        // one duration everywhere instead of one duration and one time constant.
        // Read the live level BEFORE cancelling: cancelling first snaps the param
        // back to its base value, which would jump before it fades.
        const rel = rec.release == null ? rampSec(0) : rec.release;
        const cur = Math.max(0.0002, rec.gain.gain.value);
        rec.gain.gain.cancelScheduledValues(t);
        rec.gain.gain.setValueAtTime(cur, t);
        rec.gain.gain.exponentialRampToValueAtTime(0.0001, t + rel);
        rec.node.stop(t + rel + 0.05);
        if (rec.extra) for (const e of rec.extra) e.stop(t + rel + 0.05);
      } catch (e) {}
    }
  }
  // Slide a LIVE held source to new per-voice levels (and, for Sound.glide, to a
  // new pitch) without restarting it: a smooth ramp to whatever it is handed, not
  // a stop+start. A stadium swells as an attack nears goal by returning the same
  // sound at a rising volume; an engine note tracks speed by returning it at a
  // rising pitch. Which of the two is possible is decided by heldKey.
  //
  // How fast a NEW LEVEL lands is one number with two meanings, and the split is
  // the same one as Sound.voice vs Sound.glide (ADR-0024). A bed's level is
  // supposed to lag: a crowd that tracks each event is heard as a rhythm, not as a
  // background. A VOICE's level is not: a polyphonic instrument scales its voices
  // by how many are sounding, and that has to land while the chord is still being
  // held. At 1.1s it took about three seconds to arrive, so the notes already down
  // kept their old level and a chord still went over full scale; the app's
  // normalisation could only reach the notes that had not started yet.
  //
  // 30ms is prompt enough to be inaudible as a movement and long enough not to
  // step the gain (a step is a click).
  const VOICE_RELEVEL_SEC = 0.03;
  const BED_RELEVEL_SEC = 1.1;
  function soundGlideTo(h, snd, levelSec) {
    if (!h || !h.nodes || !audioCtx || !snd || !snd.voices) return;
    const t = audioCtx.currentTime;
    for (let i = 0; i < h.nodes.length; i++) {
      const v = snd.voices[i];
      if (!v) continue;
      const rec = h.nodes[i];
      const peak = Math.max(0.0002, Math.max(0, Math.min(100, v.volume == null ? 60 : v.volume)) / 100);
      // VOLUME: retarget only when it has actually shifted (skip re-scheduling the
      // ramp 60x/s when the level is basically steady - that churn risks a zipper).
      if (rec._peak == null || Math.abs(rec._peak - peak) >= 0.004) {
        rec._peak = peak;
        try {
          // Cancel from the end of the attack, never from `now`. The attack is
          // scheduled slightly in the FUTURE (soundHeldStart starts at
          // currentTime + 0.02), so cancelling at `now` wipes it, and a
          // GainNode whose automation is wiped falls back to its base value of
          // 1, which is 2.5x the level the voice asked for. That was audible as
          // a held note jumping in volume the moment it first slid, and staying
          // there for seconds while setTargetAtTime crawled back down at the
          // bed's 1.1s lag.
          rec.gain.gain.cancelScheduledValues(Math.max(t, rec._attackEnd || 0));
          // Big time-constant = the level lags HEAVILY, following only the slow
          // trend and ignoring per-event jumps. That lag is what a BED is: a level
          // that tracks each event is heard as a rhythm, not as a background.
          // heldLevel, not peak: a held voice that asked for a sustain level is
          // sitting AT that level, and retargeting to the raw peak would jump it
          // back to full the first time anything glided it.
          rec.gain.gain.setTargetAtTime(heldLevel(v, peak), t, levelSec == null ? BED_RELEVEL_SEC : levelSec);
        } catch (e) {}
      }
      // PITCH: glide the SAME oscillator to the voice's new freq (oscillators only
      // - a noise BufferSource has no .frequency, so noise beds are untouched).
      // This is the textbook "smoothed parameter": chain setTargetAtTime toward the
      // moving target, each call gliding from the value the param currently holds.
      // Do NOT cancelScheduledValues first - cancelling an in-flight ramp snaps the
      // param back to the oscillator's intrinsic base freq, so every change dipped to
      // bass and then climbed (an audible click). Without the cancel it just slides.
      // The time-constant sets how gradual the slide is. Guarded separately from
      // volume so a freq change still glides even while the volume is steady.
      if (rec.node && rec.node.frequency && v.freq) {
        const f = Math.max(1, v.freq);
        if (rec._freq == null || Math.abs(rec._freq - f) >= 1) {
          rec._freq = f;
          try {
            rec.node.frequency.setTargetAtTime(f, t, 0.08);
          } catch (e) {}
        }
      }
    }
  }
  // Sound.loop REPLAYS a Sound seamlessly forever (melodies / background music),
  // unlike the bed which holds. A Sound is already a flat, fully-timed schedule
  // (each voice has delayMs + ms). A look-ahead ("two clocks") scheduler drives
  // it: a coarse setInterval wakes ~33x/s and books, on the WebAudio clock at
  // sample-accurate absolute times, everything that comes due inside the next
  // `horizon`. The audio clock keeps running even when the tab is backgrounded
  // (unlike Time.every), so the music doesn't stall. Voices route through a
  // private loopGain (not masterGain) so stop() can fade the whole track out
  // cleanly. Loop voices are exempt from the SFX voice pool: music is never
  // starved by effects.
  //
  // IT BOOKS A SLICE, NOT A PERIOD, and that distinction is the whole point.
  // This used to advance by the sound's full length and, on each step, book
  // EVERY voice in it. That is fine for a four-bar jingle and quietly fatal for
  // a score: Iron Meridian's pieces are three minutes long and carry ~3000
  // sounding voices, and booking them in one synchronous burst measured 197 ms
  // of node building in the running game — twelve dropped frames, once at the
  // start of a mission and again every time the loop came round. The cost is not
  // in the music, it is in scheduling a period's worth of it at a time. Now the
  // voices are sorted by onset and a cursor walks them, so a tick books the
  // handful actually due (typically single digits) and the burst is gone
  // whatever the piece costs.
  // NOT guarded on soundMuted, and that is the fix for a bug you could hear: it
  // used to return null while muted, and because `setMuted` only ramps the
  // master gain -- it does not restart subscriptions -- the reconciler went on
  // believing this sub was running. Unmuting could never bring it back, so a
  // game with one background track and a mute toggle stayed silent for the rest
  // of the session. The master gain already silences it; `tick` below still
  // books nothing while muted, so the cost of running is a timer.
  function soundLoopStart(snd) {
    const ctx = ensureAudio();
    if (!ctx || !snd || !snd.voices || snd.voices.length === 0) return null;
    // The period comes from ALL voices, rests included: a trailing Sound.rest is
    // how a piece pads its last bar, and dropping it would shorten the loop.
    let periodMs = 0;
    for (const v of snd.voices) periodMs = Math.max(periodMs, (v.delayMs || 0) + (v.ms || 0));
    // A FLOOR on the cycle, the same 50 ms iOS has always had. It is the loop's
    // version of the 20 ms floor on a note: past a point a period stops being a
    // rhythm and becomes a rate, and a Sound built from zero-length voices would
    // ask this scheduler for hundreds of starts a second. Without it the two
    // runtimes disagreed twice over -- a 40 ms cycle ran 25% faster here, and a
    // Sound whose voices all had length 0 was silence here and a 20 Hz buzz
    // there.
    periodMs = Math.max(50, periodMs);
    // Rests are then dropped from the CURSOR: scheduleVoice ignores them anyway,
    // and they are typically half the list, so carrying them would double the
    // walk for nothing. Sorted by onset, which is what makes a cursor possible.
    const due = snd.voices
      .filter((v) => v.wave !== 'Rest')
      .map((v) => ({ v, at: (v.delayMs || 0) / 1000 }))
      .sort((a, b) => a.at - b.at);
    if (due.length === 0) return null;          // a loop of pure silence
    const period = periodMs / 1000;
    const loopGain = ctx.createGain();
    loopGain.gain.value = 1;
    loopGain.connect(masterGain);
    // `origin` is the audio time at which the CURRENT pass through `due` began;
    // `i` is how far into that pass the cursor has booked. Together they are the
    // playhead. soundLoopStop only ever touches `timer` and `gain`.
    const state = { timer: null, gain: loopGain, origin: ctx.currentTime + 0.06, i: 0, period, due };
    const horizon = 0.14;    // schedule this far ahead (s)
    const wrap = () => { if (state.i >= state.due.length) { state.origin += state.period; state.i = 0; } };
    const tick = () => {
      if (!audioCtx) return;
      const now = audioCtx.currentTime;
      // While muted, keep the timer alive but book nothing, and hold the playhead
      // at the present so unmuting resumes from the top rather than bursting to
      // catch up. (Same behaviour as before: an unmute restarts the piece.)
      if (soundMuted) { state.origin = now + 0.06; state.i = 0; return; }
      // Anything already past is SKIPPED, not booked. A backgrounded tab
      // throttles setInterval, so a tick can arrive long after the last one, and
      // Web Audio clamps a start time in the past to "now" — booking the missed
      // stretch would fire all of it at once, which is precisely the burst this
      // scheduler exists to avoid. Terminates because every iteration advances
      // either the cursor or the origin.
      for (;;) {
        wrap();
        if (state.origin + state.due[state.i].at >= now) break;
        state.i++;
      }
      // Then book what is due inside the horizon. A period SHORTER than the
      // horizon simply wraps more than once here, same as the old loop did.
      for (;;) {
        wrap();
        const d = state.due[state.i];
        if (state.origin + d.at >= now + horizon) break;
        scheduleVoice(audioCtx, d.v, state.origin, loopGain);
        state.i++;
      }
    };
    tick();
    state.timer = setInterval(tick, 30);
    return state;
  }
  // Exposed for tests: the scheduler is the only part of this file whose
  // behaviour is a function of TIME, so a test needs to hold the clock and call
  // the tick itself. See TestLoopKeepsUnequalVoicesLocked.
  if (typeof globalThis !== 'undefined') globalThis.__marTestLoopStart = soundLoopStart;
  function soundLoopStop(state) {
    if (!state) return;
    if (state.timer) { clearInterval(state.timer); state.timer = null; }
    if (audioCtx && state.gain) {
      const t = audioCtx.currentTime;
      try {
        state.gain.gain.cancelScheduledValues(t);
        state.gain.gain.setTargetAtTime(0.0001, t, 0.06);   // brief fade so a mid-note cut doesn't click
      } catch (e) {}
      // ...then CUT it. setTargetAtTime is an asymptote, not a stop: it only
      // ever approaches 0.0001, and the node stays wired to master. That is
      // fine for one loop and wrong in bulk: a sub's identity is its sound, so
      // dragging a patch slider while a note holds stops and starts a loop
      // EVERY frame, and each faded-but-connected gain would pile up on master
      // (dozens per second, summing back into something audible). Disconnecting
      // after the fade makes a stop final: nothing scheduled into this node can
      // ever be heard again.
      soundDetachAfterFade(state);
    }
  }
  // Drop a faded-out gain off the graph once the fade has run (and once any
  // already-scheduled voices inside the loop's look-ahead window have played
  // out). Guarded so a double stop cannot disconnect twice.
  function soundDetachAfterFade(state) {
    if (state.detaching) return;
    state.detaching = true;
    setTimeout(() => {
      try { state.gain.disconnect(); } catch (e) {}
      state.gain = null;
    }, 400);
  }
  // Sound.once plays the whole schedule ONE time while subscribed: a death
  // dirge, a stinger: through a private gain so unsubscribing (a restart, a
  // page change) cuts it off with a click-free fade. No re-scheduling: after
  // the last voice ends the sub simply holds silence, keeping only the right
  // to cancel. The cancellable middle ground between play (fire-and-forget)
  // and loop (repeats forever).
  function soundOnceStart(snd) {
    const ctx = ensureAudio();
    // Not guarded on soundMuted: see soundLoopStart. A sub that never started
    // cannot be resumed by unmuting.
    if (!ctx || !snd || !snd.voices || snd.voices.length === 0) return null;
    const g = ctx.createGain();
    g.gain.value = 1;
    g.connect(masterGain);
    const at = ctx.currentTime + 0.02;
    for (const v of snd.voices) scheduleVoice(ctx, v, at, g);
    return { gain: g };
  }
  function soundOnceStop(state) {
    if (!state || !audioCtx || !state.gain) return;
    const t = audioCtx.currentTime;
    try {
      state.gain.gain.cancelScheduledValues(t);
      state.gain.gain.setTargetAtTime(0.0001, t, 0.06);
    } catch (e) {}
    soundDetachAfterFade(state);   // same reason as the loop: a fade is not a stop
  }

  // Device (docs/proposals/device.md): capability readings from CSS media
  // queries + the viewport, NEVER a user-agent string. matchMedia is the web's
  // own truth source for pointer precision / hover / colour-scheme / motion;
  // iPadOS lies in its UA (reports macOS) but answers these queries honestly.
  // deviceMediaQueries returns the live MediaQueryList objects so a sub can
  // attach `change` listeners to them; readDevice snapshots the current record.
  function deviceMediaQueries() {
    const mm = (typeof window !== 'undefined' && window.matchMedia)
      ? (q) => window.matchMedia(q)
      : () => null;
    return {
      coarse:  mm('(pointer: coarse)'),
      anyFine: mm('(any-pointer: fine)'),
      hover:   mm('(hover: hover)'),
      dark:    mm('(prefers-color-scheme: dark)'),
      reduce:  mm('(prefers-reduced-motion: reduce)'),
    };
  }
  // readDevice snapshots the current Device record. Safe on any host: with no
  // matchMedia / window (SSR, build) it defaults to a plain fine-pointer desktop
  //: subs only run in the browser, so that default is just belt-and-suspenders.
  function readDevice() {
    const q = deviceMediaQueries();
    const on = (mq) => !!(mq && mq.matches);
    const w = (typeof window !== 'undefined') ? (window.innerWidth | 0) : 0;
    const h = (typeof window !== 'undefined') ? (window.innerHeight | 0) : 0;
    return VRecord({
      pointer:              VCtor(on(q.coarse) ? 'Coarse' : 'Fine'),
      anyFine:              VBool(on(q.anyFine)),
      supportsHover:        VBool(on(q.hover)),
      width:                VInt(w),
      height:               VInt(h),
      prefersDark:          VBool(on(q.dark)),
      prefersReducedMotion: VBool(on(q.reduce)),
    }, ['pointer', 'anyFine', 'supportsHover', 'width', 'height', 'prefersDark', 'prefersReducedMotion']);
  }

  const subSources = {
    timeEvery: {
      // clock: true marks a "time passes on its own" source. The dev
      // time-travel panel pauses these while you inspect a past frame, so the
      // world holds still (see stripClockSubs). A future frame-loop source
      // would set clock: true too.
      clock: true,
      // GAME-RATE intervals (≤ 20 ms: the canvas games' 1000/60 = 16) ride
      // requestAnimationFrame instead of setInterval. setInterval(16) beats
      // against the display's ~16.7 ms vsync: every ~half second two ticks
      // land inside one painted frame, so a steady scroller visibly jumps a
      // double step: permanent micro-judder. With rAF, when the display's
      // frame period is within ±25% of the interval (the normal 60 Hz case)
      // we lock ONE tick per painted frame: glass-smooth constant motion
      // (ticks run at the display's ~16.7 ms, ~4% slower than nominal 16:
      // imperceptible). Off the lock, an accumulator keeps the GAME clock at
      // real time on both kinds of mismatched display (ADR-0003): on faster
      // panels (120/144 Hz) it fires every 2nd/3rd frame, and on SLOWER
      // frames (30 Hz Low Power Mode, a heavy scene) it fires 2..4
      // back-to-back catch-up ticks: the world advances at true speed and
      // only the paint rate drops, instead of dropped frames dilating game
      // time into slow motion. The burst is capped (impossible load degrades
      // to slow motion, never a catch-up spiral) and renders once (see
      // tickBurst). Longer intervals (real clocks) stay on setInterval:
      // rAF would freeze them entirely in a backgrounded tab.
      start: (g, fire) => {
        if (g.intervalMs > 20 || typeof requestAnimationFrame === 'undefined') {
          return { kind: 'int', id: setInterval(fire, g.intervalMs) };
        }
        const rec = { kind: 'raf', id: 0, prev: 0, ema: 0, acc: 0, stopped: false };
        // Max catch-up ticks per painted frame: holds true game speed down to
        // 15 display-fps (60/4); beyond that the debt clamp below drops the
        // excess. Update is the cheap half of a frame (view + reconcile
        // dominate), so a burst's extra updates are affordable by design.
        const CATCH_UP_MAX = 4;
        const step = (ts) => {
          if (rec.stopped) return;
          if (rec.prev) {
            // clamp a background-tab gap so returning doesn't burst-fire
            // more than one frame's worth of catch-up
            const d = Math.min(100, ts - rec.prev);
            rec.ema = rec.ema ? rec.ema * 0.9 + d * 0.1 : d;
            const iv = g.intervalMs;
            if (Math.abs(rec.ema - iv) <= iv / 4) {
              fire();
            } else {
              rec.acc += d;
              let n = 0;
              while (rec.acc >= iv && n < CATCH_UP_MAX) { rec.acc -= iv; n++; }
              // Debt beyond the cap is dropped; at most one interval of
              // residual carries into the next frame (slow-motion floor).
              if (rec.acc > iv) rec.acc = iv;
              if (n > 0) {
                // Intermediate ticks advance the world without painting;
                // the final tick (flag off) renders the frame once.
                tickBurst = true;
                try { for (let k = 1; k < n; k++) fire(); } finally { tickBurst = false; }
                fire();
              }
            }
          }
          rec.prev = ts;
          rec.id = requestAnimationFrame(step);
        };
        rec.id = requestAnimationFrame(step);
        return rec;
      },
      stop: (rec) => {
        const h = rec.handle;
        if (h && h.kind === 'raf') { h.stopped = true; cancelAnimationFrame(h.id); }
        else if (h && h.kind === 'int') clearInterval(h.id);
        else clearInterval(h);
      },
      fire: (rec) => {
        const now = timeOf('Time.every', Date.now());
        for (const tg of rec.taggers) deliverSub(tg, now);
      },
    },
    // Keyboard.watch: the held-key mirror. Shared window keydown/keyup keep
    // heldKeyCodes current; any change (or a blur/tab-hide clear) notifies every
    // sub, which delivers the whole set as { down : List Keyboard.Key }. Each
    // Key is VCtor(event.code): it matches the qualified pattern Keyboard.<code>
    // by bare tag; a code outside the enum just won't match a branch. Reports
    // state: each subscriber gets the current set when it starts listening (see
    // seedSub). NOT clock: input keeps flowing during time-travel inspection.
    keyboardWatch: {
      reportsState: true,
      start: (g, fire) => {
        const h = { fire };
        kbWatchFires.add(fire);
        installKbListeners();
        return h;
      },
      stop: (rec) => { const h = rec.handle; if (!h) return; kbWatchFires.delete(h.fire); },
      read: () => keyboardStateRecord(),
      fire: (rec) => deliverState(rec, rec.taggers, rec.owners, keyboardStateRecord()),
    },
    // Gamepad.watch: the full-pad mirror. The shared poll (padPoll) keeps
    // connection + both sticks + held-button set current and notifies every sub
    // on change; fire() delivers the whole snapshot record. Reports state (see
    // seedSub). NOT clock: input flows during time-travel.
    gamepadWatch: {
      reportsState: true,
      start: (g, fire) => {
        const h = { fire, stopped: false };
        padWatchAdd(fire);
        return h;
      },
      stop: (rec) => { const h = rec.handle; if (!h) return; h.stopped = true; padWatchRemove(h.fire); },
      read: () => gamepadStateRecord(),
      fire: (rec) => deliverState(rec, rec.taggers, rec.owners, gamepadStateRecord()),
    },
    // Sound.voice and Sound.glide: a held source, alive for as long as the
    // subscription is. Same start and stop; they differ in exactly two things,
    // and both are the same distinction:
    //
    //   what the reconcile key covers   heldKey, computed before the sub gets
    //                                   here: pitch is identity for a voice and
    //                                   a live parameter for a glide
    //   how fast a new level lands      a voice now, a bed slowly
    //
    // Whatever the key leaves out is retuned on the running node instead of
    // restarting it. Emits no msgs (fire is a no-op). NOT clock: audio keeps
    // going during time-travel.
    voice: {
      start: (g) => soundHeldStart(g.sound),
      stop: (rec) => soundHeldStop(rec.handle),
      update: (rec, g) => soundGlideTo(rec.handle, g.sound, VOICE_RELEVEL_SEC),
      fire: () => {},
    },
    glide: {
      start: (g) => soundHeldStart(g.sound),
      stop: (rec) => soundHeldStop(rec.handle),
      update: (rec, g) => soundGlideTo(rec.handle, g.sound, BED_RELEVEL_SEC),
      fire: () => {},
    },
    // Sound.loop: replays a Sound seamlessly while subscribed. The reconcile key
    // is the FULL content (incl. volume), so any change swaps songs (stop old,
    // start new); the same Sound keeps looping without a restart. No update hook
    // (a content change is a swap, not a live retune). NOT clock.
    loop: {
      start: (g) => soundLoopStart(g.sound),
      stop: (rec) => soundLoopStop(rec.handle),
      fire: () => {},
    },
    // Sound.once: plays the Sound a single time while subscribed; after the
    // last note the sub only holds the right to CANCEL (unsubscribing fades a
    // mid-note cut cleanly). Content change = swap. NOT clock.
    once: {
      start: (g) => soundOnceStart(g.sound),
      stop: (rec) => soundOnceStop(rec.handle),
      fire: () => {},
    },
    // Device.watch: reports live device capabilities (docs/proposals/device.md).
    // Each subscriber gets the current record when it starts listening (see
    // seedSub), then a fresh record on every change: a media-query flip (mouse
    // plugged into an iPad, dark mode at sunset) or a window resize / rotation /
    // split-view (all coalesced to a single rAF). NOT clock: capability changes
    // are external input like keys, so they keep flowing during time-travel.
    deviceWatch: {
      reportsState: true,
      start: (g, fire) => {
        const queries = deviceMediaQueries();
        const h = { queries, onChange: null, stopped: false };
        let rafPending = false;
        h.onChange = () => {
          if (rafPending) return;
          rafPending = true;
          const run = () => { rafPending = false; if (!h.stopped) fire(); };
          if (typeof requestAnimationFrame !== 'undefined') requestAnimationFrame(run); else run();
        };
        for (const k in queries) { const mq = queries[k]; if (mq && mq.addEventListener) mq.addEventListener('change', h.onChange); }
        if (typeof window !== 'undefined') window.addEventListener('resize', h.onChange);
        return h;
      },
      stop: (rec) => {
        const h = rec.handle;
        if (!h) return;
        h.stopped = true;
        for (const k in h.queries) { const mq = h.queries[k]; if (mq && mq.removeEventListener) mq.removeEventListener('change', h.onChange); }
        if (typeof window !== 'undefined') window.removeEventListener('resize', h.onChange);
      },
      read: () => readDevice(),
      fire: (rec) => deliverState(rec, rec.taggers, rec.owners, readDevice()),
    },
  };
  // Drop clock-type sub items (Time.every, …) from a Sub value. Used only by
  // the dev time-travel panel: while you're viewing a past frame, we reconcile
  // against a clock-free set so the timers stop and the world freezes. Non-clock
  // sources (external pushes, input) and async effect results keep flowing.
  function stripClockSubs(subValue) {
    if (!subValue || subValue.k !== 'SUB' || !subValue.items) return subValue;
    return { k: 'SUB', items: subValue.items.filter((it) => !(subSources[it.src] && subSources[it.src].clock)) };
  }
  // True when the dev dock's time-travel panel is the open section RIGHT NOW.
  // Opening it freezes the world's clock sources (see render) so you pause at
  // the present frame, not just when you scrub back. Reads the live session flag
  // (set by the dock's expand/collapse) rather than persisted localStorage:
  // the persisted value survives reloads and would silently freeze a fresh page.
  function timeTravelPanelOpen() {
    return timeTravelPanelIsOpen;
  }
  function teardownAllSubs() {
    for (const rec of activeSubs.values()) subSources[rec.src].stop(rec);
    activeSubs.clear();
    // Every source starts again, so every subscriber is handed its state again
    // (a hot reload runs the new update on the state as it is).
    pageStateSeen.clear();
  }
  function reconcileSubs(subValue) {
    const desired = new Map(); // key -> { src, intervalMs, sound, taggers: [], owners: [] }
    const items = (subValue && subValue.k === 'SUB' && subValue.items) ? subValue.items : [];
    for (const it of items) {
      let g = desired.get(it.key);
      if (!g) { g = { src: it.src, intervalMs: it.intervalMs, sound: it.sound, taggers: [], owners: [] }; desired.set(it.key, g); }
      g.taggers.push(it.tagger);
      g.owners.push(it.owner);
    }
    for (const [key, rec] of [...activeSubs]) {
      if (!desired.has(key)) { subSources[rec.src].stop(rec); activeSubs.delete(key); }
    }
    for (const [key, g] of desired) {
      const existing = activeSubs.get(key);
      if (existing) {
        const joined = subSources[existing.src].reportsState
          ? g.owners.filter((o) => existing.owners.indexOf(o) < 0)
          : [];
        existing.taggers = g.taggers;
        existing.owners = g.owners;
        // Same key, new payload (e.g. the same bed at a new volume): let the
        // source retune the live node instead of restarting it.
        existing.sound = g.sound;
        const up = subSources[existing.src].update;
        if (up) up(existing, g);
        seedSub(existing, joined);
      }
      else {
        const rec = { key, src: g.src, intervalMs: g.intervalMs, sound: g.sound, taggers: g.taggers, owners: g.owners, handle: null, seedFor: null };
        rec.handle = subSources[g.src].start(g, () => subSources[g.src].fire(rec));
        activeSubs.set(key, rec);
        seedSub(rec, g.owners);
      }
    }
  }

  // A source that reports a state hands it to each subscriber that starts
  // listening, once, as a dispatch of its own: a microtask, never re-entrant
  // with the render that subscribed. Who receives it is decided when it is
  // delivered, not when it was asked for: a subscriber that left in between
  // gets nothing (the page navigated away before the microtask ran), and the
  // one that replaced it gets its own, so nobody receives a seed twice or a
  // seed meant for another page. A page shown again with the model it had is
  // handed the state only if it changed (see pageStateSeen).
  //
  // A subscriber is the page or the store, not each tagger: a page that adds a
  // second tagger for a source it already listens to is handed the state on
  // the next change, like a second handler for one event.
  function seedSub(rec, owners) {
    if (!subSources[rec.src].reportsState || owners.length === 0) return;
    if (!rec.seedFor) {
      rec.seedFor = new Set();
      const flush = () => {
        const pending = rec.seedFor;
        rec.seedFor = null;
        if (activeSubs.get(rec.key) !== rec) return;
        const value = subSources[rec.src].read();
        const seen = pageStateSeen.get(subPageKey + '\u0000' + rec.key);
        const unchanged = seen !== undefined && eqValues(seen, value);
        const taggers = [], owners = [];
        rec.taggers.forEach((tg, i) => {
          const o = rec.owners[i];
          if (!pending.has(o) || (isPageOwner(o) && unchanged)) return;
          taggers.push(tg);
          owners.push(o);
        });
        if (taggers.length) deliverState(rec, taggers, owners, value);
      };
      if (typeof queueMicrotask !== 'undefined') queueMicrotask(flush); else setTimeout(flush, 0);
    }
    for (const o of owners) rec.seedFor.add(o);
  }

  // ---------- Binary operators ----------
  //
  // Every operator Mar has is a builtin, and none can be rebound: no syntax
  // binds an operator's name (the parser's operator table is closed, and a
  // parameter or a pattern variable is always an identifier). So evalExpr
  // calls these two-argument functions straight from this table, instead of
  // looking the operator up through every frame of the environment and then
  // applying it one argument at a time. makeBuiltinEnv binds the very same
  // functions under the operators' names. Mirrors evalBinop in
  // internal/runtime/eval.go.

  // Int is 53 bits wide, and this is the runtime the width was chosen FOR:
  // JavaScript has no integers, so an `Int` here is a double, and past 2^53
  // it stops being able to tell 9007199254740993 from 9007199254740992 and
  // says nothing about it. Go wrapped around and Swift trapped; only this
  // one was quietly wrong, which is the worst of the three.
  //
  // Number.isSafeInteger is exactly the predicate: an integer, and within
  // +/-(2^53-1). It is reliable even for a product that overflowed into the
  // inexact range, because rounding a double is relatively tiny: a value
  // past 2^53 never rounds back under it.
  // durationOutOfRange is what building a Duration raises when the interval
  // does not fit in Int milliseconds. The same sentence as Go's
  // durationOutOfRange (internal/runtime/intrange.go) and MarInt's.
  function durationOutOfRange(source) {
    return new Error('Duration out of range: ' + source + ' is outside the range of a Duration' +
      ' (-9007199254740991 to 9007199254740991 milliseconds)');
  }

  function checkedInt(a, op, b, c) {
    if (!Number.isSafeInteger(c)) {
      throw new Error('Int overflow: ' + a + ' ' + op + ' ' + b +
        ' is outside the range of Int (-9007199254740991 to 9007199254740991)');
    }
    return VInt(c);
  }

  const BINOPS = Object.create(null);
  // Arithmetic: `+ - *` close over both Int and Decimal (the
  // typechecker's `number` constraint guarantees the operands agree).
  BINOPS['+'] = (a, b) => a.k === 'De' ? decAddV(a, b) : checkedInt(a.n, '+', b.n, a.n + b.n);
  BINOPS['-'] = (a, b) => a.k === 'De' ? decSubV(a, b) : checkedInt(a.n, '-', b.n, a.n - b.n);
  BINOPS['*'] = (a, b) => a.k === 'De' ? decMulV(a, b) : checkedInt(a.n, '*', b.n, a.n * b.n);
  // `//`, truncating Int division, total: /0 yields 0 on every runtime.
  BINOPS['//'] = (a, b) => VInt(b.n === 0 ? 0 : Math.trunc(a.n / b.n));
  // `/`, Decimal-only, and it produces a QUESTION, not a number: the
  // inert exact quotient, resolved by Decimal.rounded / exact /
  // withRemainder, which is where the precision gets written.
  BINOPS['/'] = (a, b) => VDivision(a, b);
  // Comparisons
  BINOPS['=='] = (a, b) => VBool(eqValues(a, b));
  BINOPS['/='] = (a, b) => VBool(!eqValues(a, b));
  BINOPS['<'] = (a, b) => VBool(cmpValues(a, b) < 0);
  BINOPS['>'] = (a, b) => VBool(cmpValues(a, b) > 0);
  BINOPS['<='] = (a, b) => VBool(cmpValues(a, b) <= 0);
  BINOPS['>='] = (a, b) => VBool(cmpValues(a, b) >= 0);
  // Logic. evalExpr short-circuits `&&` and `||` itself and never calls
  // these two; they are what the names are bound to.
  BINOPS['&&'] = (a, b) => VBool(a.b && b.b);
  BINOPS['||'] = (a, b) => VBool(a.b || b.b);
  // Append (strings + lists)
  BINOPS['++'] = (a, b) => {
    if (a.k === 'S') return VString(a.s + b.s);
    if (a.k === 'L') return VList(a.xs.concat(b.xs));
    throw new Error('++: unsupported');
  };
  // Cons
  BINOPS['::'] = (h, t) => VList([h].concat(t.xs));
  // Pipes
  BINOPS['|>'] = (x, f) => apply(f, x);
  BINOPS['<|'] = (f, x) => apply(f, x);

  function binopNative(op) {
    const f = BINOPS[op];
    return native(2, ([a, b]) => f(a, b));
  }

  function makeBuiltinEnv() {
    const env = envNew(null);
    const def = (n, v) => envDefine(env, n, v);

    // Booleans / Maybe / Result / Order constructors. Order (LT/EQ/GT)
    // is used by List.sortWith: same convention as Elm.
    def('True',  VBool(true));
    def('False', VBool(false));
    def('Nothing', VCtor('Nothing'));
    def('Just', native(1, args => VCtor('Just', [args[0]])));
    def('Ok',  native(1, args => VCtor('Ok',  [args[0]])));
    def('Err', native(1, args => VCtor('Err', [args[0]])));
    def('LT', VCtor('LT'));
    def('EQ', VCtor('EQ'));
    def('GT', VCtor('GT'));

    // Method constructors: the HTTP verbs passed to Service.declare.
    def('GET', VCtor('GET'));
    def('POST', VCtor('POST'));
    def('PUT', VCtor('PUT'));
    def('PATCH', VCtor('PATCH'));
    def('DELETE', VCtor('DELETE'));

    // Service.Error constructors: the transport failure a Service.call
    // delivers in its Err. serviceCall builds these directly (see
    // serviceErrorFromResponse / serviceErrorOffline). Registered under
    // their qualified names only, the Elm Http.Error model: user code
    // writes `Service.Offline` to construct and to pattern-match, and the
    // bare names stay free for user constructors. Tags stay bare.
    // Service.Offline / Unauthorized / ServerError constructors come from the
    // generated registry below.
    def('serviceErrorToString', native(1, ([e]) => VString(serviceErrorToStringJS(e))));
    def('Service.errorToString', envLookup(env, 'serviceErrorToString'));

    // The operators. Their implementations live in BINOPS (above), which is
    // also where evalExpr calls them from; these bindings are the same
    // functions under their names.
    def('+', binopNative('+'));
    def('-', binopNative('-'));
    def('*', binopNative('*'));
    def('//', binopNative('//'));
    def('/', binopNative('/'));
    def('==', binopNative('=='));
    def('/=', binopNative('/='));
    def('<',  binopNative('<'));
    def('>',  binopNative('>'));
    def('<=', binopNative('<='));
    def('>=', binopNative('>='));
    def('&&', binopNative('&&'));
    def('||', binopNative('||'));
    def('++', binopNative('++'));
    def('::', binopNative('::'));
    def('|>', binopNative('|>'));
    def('<|', binopNative('<|'));

    // Decimal stdlib: semantics match internal/runtime/decimal.go
    // exactly (the conformance vectors in decimal_test.go must agree).
    const decimalZeroV = VDec(0n, 0);
    def('decimalZero', decimalZeroV);
    def('Decimal.zero', decimalZeroV);

    const decimalFromIntImpl = native(1, ([n]) => VDec(BigInt(n.n), 0));
    def('decimalFromInt', decimalFromIntImpl);
    def('Decimal.fromInt', decimalFromIntImpl);

    const decimalFromCentsImpl = native(1, ([n]) => VDec(BigInt(n.n), 2));
    def('decimalFromCents', decimalFromCentsImpl);
    def('Decimal.fromCents', decimalFromCentsImpl);

    const decimalToCentsImpl = native(1, ([d]) => {
      if (d.scale > 2) throw new Error('Decimal.toCents: value has scale ' + d.scale + ' (more than 2 places); round explicitly first with Decimal.toScale');
      const r = decToScaleV(d, 2, 'Down');
      const n = Number(r.coef);
      if (!Number.isSafeInteger(n)) throw new Error('Decimal.toCents: value does not fit an Int');
      return VInt(n);
    });
    def('decimalToCents', decimalToCentsImpl);
    def('Decimal.toCents', decimalToCentsImpl);

    const decimalTruncateImpl = native(1, ([d]) => VInt(decToIntV(d, 'Down')));
    def('decimalTruncate', decimalTruncateImpl);
    def('Decimal.truncate', decimalTruncateImpl);

    const decimalRoundImpl = native(1, ([d]) => VInt(decToIntV(d, 'HalfEven')));
    def('decimalRound', decimalRoundImpl);
    def('Decimal.round', decimalRoundImpl);

    const decimalFloorImpl = native(1, ([d]) => VInt(decToIntV(d, 'Floor')));
    def('decimalFloor', decimalFloorImpl);
    def('Decimal.floor', decimalFloorImpl);

    const decimalCeilingImpl = native(1, ([d]) => VInt(decToIntV(d, 'Ceiling')));
    def('decimalCeiling', decimalCeilingImpl);
    def('Decimal.ceiling', decimalCeilingImpl);

    const decimalToIntWithImpl = native(2, ([mode, d]) => VInt(decToIntV(d, decRoundingTag(mode))));
    def('decimalToIntWith', decimalToIntWithImpl);
    def('Decimal.toIntWith', decimalToIntWithImpl);

    const decimalToScaleImpl = native(3, ([mode, scale, d]) => decToScaleV(d, scale.n, decRoundingTag(mode)));
    def('decimalToScale', decimalToScaleImpl);
    def('Decimal.toScale', decimalToScaleImpl);

    const decimalAbsImpl = native(1, ([d]) => VDec(d.coef < 0n ? -d.coef : d.coef, d.scale));
    def('decimalAbs', decimalAbsImpl);
    def('Decimal.abs', decimalAbsImpl);

    const decimalNegateImpl = native(1, ([d]) => VDec(-d.coef, d.scale));
    def('decimalNegate', decimalNegateImpl);
    def('Decimal.negate', decimalNegateImpl);

    const decimalCompareImpl = native(2, ([a, b]) => {
      const c = decCmpV(a, b);
      return VCtor(c < 0 ? 'LT' : c > 0 ? 'GT' : 'EQ');
    });
    def('decimalCompare', decimalCompareImpl);
    def('Decimal.compare', decimalCompareImpl);

    const decimalFromStringImpl = native(1, ([s]) => {
      const d = parseDecString(s.s);
      return d ? VCtor('Just', [d]) : VCtor('Nothing');
    });
    def('decimalFromString', decimalFromStringImpl);
    def('Decimal.fromString', decimalFromStringImpl);

    const decimalToStringImpl = native(1, ([d]) => VString(decStr(d)));
    def('decimalToString', decimalToStringImpl);
    def('Decimal.toString', decimalToStringImpl);

    // Division resolvers: the ONLY exits from Decimal.Division, and
    // the only places rounding can happen. No implicit rounding.
    const decimalRoundedImpl = native(3, ([mode, scale, dv]) => {
      const tag = decRoundingTag(mode);
      if (scale.n < 0) throw new Error('Decimal.rounded: negative scale ' + scale.n);
      if (scale.n > MAX_DECIMAL_PLACES) throw errScaleArgument('Decimal.rounded', scale.n);
      if (dv.den.coef === 0n) return VDec(0n, scale.n); // total, matching Int's `//`
      return decRoundQuotient(dv.num, dv.den, scale.n, tag);
    });
    def('decimalRounded', decimalRoundedImpl);
    def('Decimal.rounded', decimalRoundedImpl);

    const decimalWithRemainderImpl = native(2, ([scale, dv]) => {
      if (scale.n < 0) throw new Error('Decimal.withRemainder: negative scale ' + scale.n);
      if (scale.n > MAX_DECIMAL_PLACES) throw errScaleArgument('Decimal.withRemainder', scale.n);
      const q = dv.den.coef === 0n
        ? VDec(0n, scale.n)
        : decRoundQuotient(dv.num, dv.den, scale.n, 'Down');
      // remainder = a - q*b, exactly, so q * b + r == a holds by
      // construction. q*b is not fitted on its own, and is formed from q at n
      // places (the zeros the fit took from a quotient over 34 digits put
      // back), so the remainder carries max(scale a, n + scale b) places,
      // fitted (decimal.go says why).
      const qc = q.coef * decPow10(scale.n - q.scale);
      const [ca, cb, rs] = decAlign(dv.num, { coef: qc * dv.den.coef, scale: scale.n + dv.den.scale });
      const r = decFit('Decimal.withRemainder', ca - cb, rs);
      return VRecord({ quotient: q, remainder: r }, ['quotient', 'remainder']);
    });
    def('decimalWithRemainder', decimalWithRemainderImpl);
    def('Decimal.withRemainder', decimalWithRemainderImpl);

    // String stdlib: semantics match the Go runtime (and iOS Swift)
    // exactly. Arg order in particular: needle/prefix/sep first, so
    // pipe-friendly:  `s |> String.contains "foo"`.
    def('stringFromInt', native(1, ([n]) => VString(String(n.n))));
    def('String.fromInt', native(1, ([n]) => VString(String(n.n))));
    def('stringLength', native(1, ([s]) => VInt(s.s.length)));
    def('String.length', native(1, ([s]) => VInt(scalarLength(s.s))));

    const stringContainsImpl = native(2, ([needle, hay]) => VBool(hay.s.includes(needle.s)));
    def('stringContains', stringContainsImpl);
    def('String.contains', stringContainsImpl);

    const stringStartsWithImpl = native(2, ([prefix, s]) => VBool(s.s.startsWith(prefix.s)));
    def('stringStartsWith', stringStartsWithImpl);
    def('String.startsWith', stringStartsWithImpl);

    const stringToUpperImpl = native(1, ([s]) => VString(s.s.toUpperCase()));
    def('stringToUpper', stringToUpperImpl);
    def('String.toUpper', stringToUpperImpl);

    const stringToLowerImpl = native(1, ([s]) => VString(s.s.toLowerCase()));
    def('stringToLower', stringToLowerImpl);
    def('String.toLower', stringToLowerImpl);

    // String.split mirrors Go's strings.Split: empty separator yields
    // one element per code unit (not per UTF-16 surrogate pair), since
    // we already use s.length elsewhere: keep behavior consistent.
    const stringSplitImpl = native(2, ([sep, s]) => {
      const parts = sep.s === '' ? Array.from(s.s) : s.s.split(sep.s);
      return VList(parts.map(p => VString(p)));
    });
    def('stringSplit', stringSplitImpl);
    def('String.split', stringSplitImpl);

    const stringJoinImpl = native(2, ([sep, list]) =>
      VString(list.xs.map(e => e.s).join(sep.s)));
    def('stringJoin', stringJoinImpl);
    def('String.join', stringJoinImpl);

    // String.trim: strip leading + trailing whitespace including
    // newlines, matching strings.TrimSpace in Go.
    const stringTrimImpl = native(1, ([s]) => VString(s.s.trim()));
    def('stringTrim', stringTrimImpl);
    def('String.trim', stringTrimImpl);

    // String.endsWith : String suffix -> String s -> Bool
    const stringEndsWithImpl = native(2, ([suf, s]) =>
      VBool(s.s.endsWith(suf.s)));
    def('stringEndsWith', stringEndsWithImpl);
    def('String.endsWith', stringEndsWithImpl);

    // String.toInt : String -> Maybe Int, Number.isInteger after
    // Number(s) rejects floats, NaN, whitespace-only strings (Number
    // would happily return 0 on " ": we don't want that). parseInt
    // would accept "12abc" as 12; Number(...) doesn't, which matches
    // Elm's stricter behavior.
    const stringToIntImpl = native(1, ([s]) => {
      const trimmed = s.s.trim();
      if (trimmed === '') return VCtor('Nothing');
      const n = Number(trimmed);
      // A number too big to BE an Int is a parse failure like any other, not
      // an error: the type already says this text might not be a number.
      // isSafeInteger folds the integer check and the range check into one.
      if (!Number.isSafeInteger(n)) return VCtor('Nothing');
      return VCtor('Just', [VInt(n)]);
    });
    def('stringToInt', stringToIntImpl);
    def('String.toInt', stringToIntImpl);

    // String.replace : String needle -> String replacement -> String s -> String
    // String.replace is join after . split before. With an EMPTY needle that
    // split has to be the one Mar means, by scalar: JavaScript's own split('')
    // cuts by UTF-16 unit and would tear a surrogate pair in half, returning a
    // string carrying two lone surrogates. Spreading iterates by code point.
    const stringReplaceImpl = native(3, ([needle, rep, s]) =>
      VString((needle.s === '' ? [...s.s] : s.s.split(needle.s)).join(rep.s)));
    def('stringReplace', stringReplaceImpl);
    def('String.replace', stringReplaceImpl);

    // String.repeat : Int -> String -> String
    const stringRepeatImpl = native(2, ([n, s]) => {
      if (n.n <= 0) return VString('');
      return VString(s.s.repeat(n.n));
    });
    def('stringRepeat', stringRepeatImpl);
    def('String.repeat', stringRepeatImpl);

    // String.padLeft / padRight: pad with a SINGLE Char (Elm-style).
    // The seed is one scalar that we repeat to fill.
    // A Mar String is a sequence of Unicode SCALAR VALUES, so every length,
    // width and position below counts scalars (ADR 0038). JavaScript stores a
    // string as UTF-16 units, which is a different number for anything above
    // the BMP: s.length is 4 for one Brazilian flag, whose scalar count is 2.
    //
    // The guard is what makes this affordable. When a string holds no
    // surrogate its UTF-16 length already IS its scalar count, and the native
    // scan settles that in ~12ns at any length; only a string that actually
    // carries a pair pays for the loop.
    const scalarLength = (s) => {
      if (!SURROGATE.test(s)) return s.length;
      let n = 0;
      for (let i = 0; i < s.length; i++) {
        if ((s.charCodeAt(i) & 0xFC00) !== 0xDC00) n++;
      }
      return n;
    };

    function padString(s, width, padCh, left) {
      const have = scalarLength(s);
      if (have >= width) return s;
      // Built by repetition rather than by slicing to a UTF-16 width, which
      // would cut a surrogate pair in half when the pad character is itself
      // above the BMP and emit a lone surrogate.
      const filler = String.fromCodePoint(padCh).repeat(width - have);
      return left ? filler + s : s + filler;
    }
    const stringPadLeftImpl = native(3, ([w, pad, s]) =>
      VString(padString(s.s, w.n, pad.c, true)));
    def('stringPadLeft', stringPadLeftImpl);
    def('String.padLeft', stringPadLeftImpl);

    const stringPadRightImpl = native(3, ([w, pad, s]) =>
      VString(padString(s.s, w.n, pad.c, false)));
    def('stringPadRight', stringPadRightImpl);
    def('String.padRight', stringPadRightImpl);

    // String.indexes : String needle -> String s -> List Int, every
    // (non-overlapping) position, counted in scalars like every other
    // position in the language. The scan still walks UTF-16 units, because
    // indexOf does; only what it REPORTS is converted.
    const stringIndexesImpl = native(2, ([needle, s]) => {
      if (needle.s === '') return VList([]);
      const out = [];
      let i = 0;             // UTF-16 offset the next scan starts at
      let scalarsBefore = 0; // scalars to the left of i
      while (true) {
        const j = s.s.indexOf(needle.s, i);
        if (j < 0) break;
        scalarsBefore += scalarLength(s.s.slice(i, j));
        out.push(VInt(scalarsBefore));
        scalarsBefore += scalarLength(needle.s);
        i = j + needle.s.length;
      }
      return VList(out);
    });
    def('stringIndexes', stringIndexesImpl);
    def('String.indexes', stringIndexesImpl);

    // List stdlib
    def('listLength', native(1, ([l]) => VInt(l.xs.length)));
    def('List.length', native(1, ([l]) => VInt(l.xs.length)));
    def('listMap', native(2, ([fn, l]) => VList(l.xs.map(x => apply(fn, x)))));
    def('List.map', native(2, ([fn, l]) => VList(l.xs.map(x => apply(fn, x)))));
    // listSum / listProduct : List number -> number. A non-empty list
    // decides itself: all elements share one type, so the first one
    // settles it, which keeps the runtime right even where a call site
    // was never elaborated. The empty list has nothing to inspect, so its
    // answer is the `empty` the typechecker picked by naming the impl.
    const numericFold = (who, dec, int, empty) => native(1, ([l]) => {
      if (l.xs.length === 0) return empty();
      if (l.xs[0].k === 'De') {
        let acc = l.xs[0];
        for (const e of l.xs.slice(1)) {
          if (e.k !== 'De') throw new Error(who + ': mixed element types');
          acc = dec(acc, e);
        }
        return acc;
      }
      let acc = l.xs[0].n;
      for (const e of l.xs.slice(1)) {
        if (e.k !== 'I') throw new Error(who + ': mixed element types');
        acc = int(acc, e.n);
      }
      return VInt(acc);
    });
    const listSumImpl = numericFold('listSum', decAddV, (a, b) => a + b, () => VInt(0));
    def('listSum', listSumImpl);
    def('listSumDecimal', numericFold('listSum', decAddV, (a, b) => a + b, () => VDec(0n, 0)));
    def('List.sum', listSumImpl);
    def('listFilter', native(2, ([fn, l]) => VList(l.xs.filter(x => apply(fn, x).b))));
    def('List.filter', native(2, ([fn, l]) => VList(l.xs.filter(x => apply(fn, x).b))));
    // List.reverse, non-mutating: build a new list rather than
    // calling Array.prototype.reverse (which mutates the underlying
    // array, surprising callers that share the same VList instance).
    const listReverseImpl = native(1, ([l]) => VList(l.xs.slice().reverse()));
    def('listReverse', listReverseImpl);
    def('List.reverse', listReverseImpl);

    // List.foldl : (a -> b -> b) -> b -> List a -> b
    const listFoldlImpl = native(3, ([fn, init, l]) => {
      let acc = init;
      for (const x of l.xs) acc = applyMany(fn, [x, acc]);
      return acc;
    });
    def('listFoldl', listFoldlImpl);
    def('List.foldl', listFoldlImpl);

    // List.range : Int -> Int -> List Int (inclusive both ends; empty when from > to)
    const listRangeImpl = native(2, ([from, to]) => {
      const a = from.n, b = to.n;
      if (a > b) return VList([]);
      const xs = new Array(b - a + 1);
      for (let i = 0; i <= b - a; i++) xs[i] = VInt(a + i);
      return VList(xs);
    });
    def('listRange', listRangeImpl);
    def('List.range', listRangeImpl);

    const listHeadImpl = native(1, ([l]) =>
      l.xs.length === 0 ? VCtor('Nothing', []) : VCtor('Just', [l.xs[0]]));
    def('listHead', listHeadImpl);
    def('List.head', listHeadImpl);

    const listTailImpl = native(1, ([l]) =>
      l.xs.length === 0 ? VCtor('Nothing', []) : VCtor('Just', [VList(l.xs.slice(1))]));
    def('listTail', listTailImpl);
    def('List.tail', listTailImpl);

    const listIsEmptyImpl = native(1, ([l]) => VBool(l.xs.length === 0));
    def('listIsEmpty', listIsEmptyImpl);
    def('List.isEmpty', listIsEmptyImpl);

    // listTake / listDrop : Int -> List a -> List a
    const listTakeImpl = native(2, ([n, l]) => {
      const k = n.n;
      if (k <= 0) return VList([]);
      if (k >= l.xs.length) return l;
      return VList(l.xs.slice(0, k));
    });
    def('listTake', listTakeImpl);
    def('List.take', listTakeImpl);

    const listDropImpl = native(2, ([n, l]) => {
      const k = n.n;
      if (k <= 0) return l;
      if (k >= l.xs.length) return VList([]);
      return VList(l.xs.slice(k));
    });
    def('listDrop', listDropImpl);
    def('List.drop', listDropImpl);

    // listMove : Int -> Int -> List a -> List a
    // Pure splice, mirrors the Go impl: defensive no-op on
    // from == to or out-of-bounds indices. Returns a NEW list;
    // never mutates the input. Used by the `list` UI primitive's
    // reorder gesture: the renderer fires onMove(from, to) and the
    // app applies this to its model.
    const listMoveImpl = native(3, ([fromV, toV, l]) => {
      const from = fromV.n;
      const to = toV.n;
      const n = l.xs.length;
      if (from === to || from < 0 || from >= n || to < 0 || to >= n) {
        return l;
      }
      const out = l.xs.slice();
      const [elt] = out.splice(from, 1);
      out.splice(to, 0, elt);
      return VList(out);
    });
    def('listMove', listMoveImpl);
    def('List.move', listMoveImpl);

    // listMember : a -> List a -> Bool, structural equality.
    const listMemberImpl = native(2, ([needle, l]) => {
      for (const e of l.xs) if (eqValues(needle, e)) return VBool(true);
      return VBool(false);
    });
    def('listMember', listMemberImpl);
    def('List.member', listMemberImpl);

    // listAny / listAll: short-circuit.
    const listAnyImpl = native(2, ([fn, l]) => {
      for (const e of l.xs) if (apply(fn, e).b) return VBool(true);
      return VBool(false);
    });
    def('listAny', listAnyImpl);
    def('List.any', listAnyImpl);

    const listAllImpl = native(2, ([fn, l]) => {
      for (const e of l.xs) if (!apply(fn, e).b) return VBool(false);
      return VBool(true);
    });
    def('listAll', listAllImpl);
    def('List.all', listAllImpl);

    // listFoldr : (a -> b -> b) -> b -> List a -> b
    const listFoldrImpl = native(3, ([fn, acc, l]) => {
      for (let i = l.xs.length - 1; i >= 0; i--) {
        acc = applyMany(fn, [l.xs[i], acc]);
      }
      return acc;
    });
    def('listFoldr', listFoldrImpl);
    def('List.foldr', listFoldrImpl);

    // listIndexedMap : (Int -> a -> b) -> List a -> List b
    // A plain loop rather than Array.prototype.map: the callback frame and
    // map's own builtin frame would sit on the host stack under every nested
    // Mar call that runs through here.
    const listIndexedMapImpl = native(2, ([fn, l]) => {
      const out = [];
      for (let i = 0; i < l.xs.length; i++) out.push(applyMany(fn, [VInt(i), l.xs[i]]));
      return VList(out);
    });
    def('listIndexedMap', listIndexedMapImpl);
    def('List.indexedMap', listIndexedMapImpl);

    // listRepeat : Int -> a -> List a
    const listRepeatImpl = native(2, ([n, v]) => {
      const k = n.n;
      if (k <= 0) return VList([]);
      const out = new Array(k);
      for (let i = 0; i < k; i++) out[i] = v;
      return VList(out);
    });
    def('listRepeat', listRepeatImpl);
    def('List.repeat', listRepeatImpl);

    // listIntersperse : a -> List a -> List a
    const listIntersperseImpl = native(2, ([sep, l]) => {
      const n = l.xs.length;
      if (n <= 1) return l;
      const out = [];
      for (let i = 0; i < n; i++) {
        if (i > 0) out.push(sep);
        out.push(l.xs[i]);
      }
      return VList(out);
    });
    def('listIntersperse', listIntersperseImpl);
    def('List.intersperse', listIntersperseImpl);

    // listPartition : (a -> Bool) -> List a -> (List a, List a)
    const listPartitionImpl = native(2, ([fn, l]) => {
      const yes = [];
      const no = [];
      for (const e of l.xs) (apply(fn, e).b ? yes : no).push(e);
      return VTuple([VList(yes), VList(no)]);
    });
    def('listPartition', listPartitionImpl);
    def('List.partition', listPartitionImpl);

    // listConcatMap : (a -> List b) -> List a -> List b
    const listConcatMapImpl = native(2, ([fn, l]) => {
      const out = [];
      for (const e of l.xs) {
        const inner = apply(fn, e);
        for (const x of inner.xs) out.push(x);
      }
      return VList(out);
    });
    def('listConcatMap', listConcatMapImpl);
    def('List.concatMap', listConcatMapImpl);

    // listFilterMap : (a -> Maybe b) -> List a -> List b
    const listFilterMapImpl = native(2, ([fn, l]) => {
      const out = [];
      for (const e of l.xs) {
        const v = apply(fn, e);
        if (v && v.k === 'C' && v.tag === 'Just' && v.args.length === 1) {
          out.push(v.args[0]);
        }
      }
      return VList(out);
    });
    def('listFilterMap', listFilterMapImpl);
    def('List.filterMap', listFilterMapImpl);

    // listMaximum / listMinimum : List comparable -> Maybe comparable,
    // over cmpValues. Nothing means EMPTY and nothing else: a comparison
    // that cannot be made now throws out of cmpValues rather than being
    // read as "equal", because answering Nothing for a three-element list
    // would be the runtime lying about which of the two situations it met.
    // The Comparable constraint on the signature is what makes that
    // failure unreachable from a checked program.
    function listExtremum(l, want) {
      if (l.xs.length === 0) return VCtor('Nothing');
      let best = l.xs[0];
      for (let i = 1; i < l.xs.length; i++) {
        const c = cmpValues(best, l.xs[i]);
        if ((want === 'max' && c < 0) || (want === 'min' && c > 0)) best = l.xs[i];
      }
      return VCtor('Just', [best]);
    }
    const listMaximumImpl = native(1, ([l]) => listExtremum(l, 'max'));
    def('listMaximum', listMaximumImpl);
    def('List.maximum', listMaximumImpl);
    const listMinimumImpl = native(1, ([l]) => listExtremum(l, 'min'));
    def('listMinimum', listMinimumImpl);
    def('List.minimum', listMinimumImpl);

    const listProductImpl = numericFold('listProduct', decMulV, (a, b) => a * b, () => VInt(1));
    def('listProduct', listProductImpl);
    def('List.product', listProductImpl);
    def('listProductDecimal', numericFold('listProduct', decMulV, (a, b) => a * b, () => VDec(1n, 0)));

    // listSort / listSortBy / listSortWith: Array.prototype.sort is
    // stable in all modern JS engines (ES2019+), so insertion order
    // survives equal keys. Same semantics as Go's sort.SliceStable.
    const listSortImpl = native(1, ([l]) =>
      VList([...l.xs].sort(cmpValues)));
    def('listSort', listSortImpl);
    def('List.sort', listSortImpl);

    const listSortByImpl = native(2, ([fn, l]) => {
      // Cache keys so we don't run `fn` O(n log n) times.
      const keys = l.xs.map(e => apply(fn, e));
      const idx = l.xs.map((_, i) => i);
      idx.sort((a, b) => cmpValues(keys[a], keys[b]));
      return VList(idx.map(i => l.xs[i]));
    });
    def('listSortBy', listSortByImpl);
    def('List.sortBy', listSortByImpl);

    // listSortWith : (a -> a -> Order) -> List a -> List a
    // Comparator returns LT / EQ / GT (a 3-way ADT): translate to
    // -1/0/1 inside the JS sort callback. `default` covers a comparator
    // that returned something that isn't an Order ctor (typecheck
    // should catch this, but the runtime guard keeps the failure mode
    // localized rather than producing nonsense ordering).
    //
    // The comparator callback stays small (orderSign does the reading after
    // the call has returned): its frame is on the host stack under every
    // nested Mar call the comparator makes.
    const orderSign = (r) => {
      if (r.k === 'C') {
        switch (r.tag) {
          case 'LT': return -1;
          case 'EQ': return 0;
          case 'GT': return 1;
        }
      }
      throw new Error('List.sortWith: comparator did not return Order (LT/EQ/GT)');
    };
    const listSortWithImpl = native(2, ([fn, l]) =>
      VList([...l.xs].sort((a, b) => {
        const args = [a, b];
        return orderSign(applyMany(fn, args));
      })));
    def('listSortWith', listSortWithImpl);
    def('List.sortWith', listSortWithImpl);

    const listConcatImpl = native(1, ([l]) => {
      const out = [];
      for (const inner of l.xs) {
        for (const x of inner.xs) out.push(x);
      }
      return VList(out);
    });
    def('listConcat', listConcatImpl);
    def('List.concat', listConcatImpl);

    // Maybe
    def('maybeWithDefault', native(2, ([def_, m]) => m.tag === 'Just' ? m.args[0] : def_));
    def('Maybe.withDefault', native(2, ([def_, m]) => m.tag === 'Just' ? m.args[0] : def_));

    const maybeMapImpl = native(2, ([fn, m]) =>
      m.tag === 'Just' ? VCtor('Just', [apply(fn, m.args[0])]) : m);
    def('maybeMap', maybeMapImpl);
    def('Maybe.map', maybeMapImpl);

    const maybeAndThenImpl = native(2, ([fn, m]) =>
      m.tag === 'Just' ? apply(fn, m.args[0]) : m);
    def('maybeAndThen', maybeAndThenImpl);
    def('Maybe.andThen', maybeAndThenImpl);

    // Result
    const resultMapImpl = native(2, ([fn, r]) =>
      r.tag === 'Ok' ? VCtor('Ok', [apply(fn, r.args[0])]) : r);
    def('resultMap', resultMapImpl);
    def('Result.map', resultMapImpl);

    const resultAndThenImpl = native(2, ([fn, r]) =>
      r.tag === 'Ok' ? apply(fn, r.args[0]) : r);
    def('resultAndThen', resultAndThenImpl);
    def('Result.andThen', resultAndThenImpl);

    const resultMapErrorImpl = native(2, ([fn, r]) =>
      r.tag === 'Err' ? VCtor('Err', [apply(fn, r.args[0])]) : r);
    def('resultMapError', resultMapErrorImpl);
    def('Result.mapError', resultMapErrorImpl);

    // Result.withDefault : a -> Result e a -> a
    const resultWithDefaultImpl = native(2, ([fallback, r]) =>
      r.tag === 'Ok' && r.args.length === 1 ? r.args[0] : fallback);
    def('resultWithDefault', resultWithDefaultImpl);
    def('Result.withDefault', resultWithDefaultImpl);

    // Result.fromMaybe : err -> Maybe a -> Result err a
    const resultFromMaybeImpl = native(2, ([err, m]) =>
      m.tag === 'Just' && m.args.length === 1
        ? VCtor('Ok', [m.args[0]])
        : VCtor('Err', [err]));
    def('resultFromMaybe', resultFromMaybeImpl);
    def('Result.fromMaybe', resultFromMaybeImpl);

    // Result.toMaybe: discards the error info (matches Elm).
    const resultToMaybeImpl = native(1, ([r]) =>
      r.tag === 'Ok' && r.args.length === 1
        ? VCtor('Just', [r.args[0]])
        : VCtor('Nothing'));
    def('resultToMaybe', resultToMaybeImpl);
    def('Result.toMaybe', resultToMaybeImpl);

    // Maybe.map2 / map3
    const maybeMap2Impl = native(3, ([fn, a, b]) => {
      if (a.tag !== 'Just' || b.tag !== 'Just') return VCtor('Nothing');
      return VCtor('Just', [applyMany(fn, [a.args[0], b.args[0]])]);
    });
    def('maybeMap2', maybeMap2Impl);
    def('Maybe.map2', maybeMap2Impl);

    const maybeMap3Impl = native(4, ([fn, a, b, c]) => {
      if (a.tag !== 'Just' || b.tag !== 'Just' || c.tag !== 'Just') return VCtor('Nothing');
      return VCtor('Just', [applyMany(fn, [a.args[0], b.args[0], c.args[0]])]);
    });
    def('maybeMap3', maybeMap3Impl);
    def('Maybe.map3', maybeMap3Impl);

    // Maybe.andMap : Maybe a -> Maybe (a -> b) -> Maybe b
    const maybeAndMapImpl = native(2, ([val, fn]) => {
      if (val.tag !== 'Just' || fn.tag !== 'Just') return VCtor('Nothing');
      return VCtor('Just', [apply(fn.args[0], val.args[0])]);
    });
    def('maybeAndMap', maybeAndMapImpl);
    def('Maybe.andMap', maybeAndMapImpl);

    // Maybe.filter : (a -> Bool) -> Maybe a -> Maybe a
    const maybeFilterImpl = native(2, ([fn, m]) => {
      if (m.tag !== 'Just' || m.args.length !== 1) return VCtor('Nothing');
      return apply(fn, m.args[0]).b ? m : VCtor('Nothing');
    });
    def('maybeFilter', maybeFilterImpl);
    def('Maybe.filter', maybeFilterImpl);

    // always : a -> b -> a, Elm's Basics.always (constant function).
    def('always', native(2, ([a]) => a));

    // not : Bool -> Bool, Elm's Basics.not. Mar has no prefix operator,
    // so negation is an ordinary function.
    def('not', native(1, ([b]) => VBool(!b.b)));

    // The numeric kit, bare and Elm-named. max/min/clamp go through
    // cmpValues, the same ordering `<` uses, so Comparable is one
    // definition of order across Int, Decimal, String and Char.
    def('max', native(2, ([a, b]) => (cmpValues(a, b) >= 0 ? a : b)));
    def('min', native(2, ([a, b]) => (cmpValues(a, b) <= 0 ? a : b)));
    // clamp low high x. Crossed bounds pin to low: the caller's bug, but
    // total either way.
    def('clamp', native(3, ([low, high, x]) => {
      if (cmpValues(x, low) <= 0) return low;
      if (cmpValues(x, high) >= 0) return high;
      return x;
    }));
    def('abs', native(1, ([v]) => {
      if (v.k === 'De') return VDec(v.coef < 0n ? -v.coef : v.coef, v.scale);
      return VInt(v.n < 0n ? -v.n : v.n);
    }));
    // modBy takes the DIVISOR's sign (floor modulo), remainderBy the
    // DIVIDEND's (truncated, in step with `//`). Both total at 0.
    //
    // Every comparison here is against `0`, never `0n`. An Int's `.n` is a
    // plain Number in this runtime: the overflow check is
    // Number.isSafeInteger, so `x === 0n` is false for every Int that exists.
    // These guards were written with BigInt literals and were therefore dead
    // code: `modBy 0` fell through to `x % 0` and put a NaN inside a value
    // typed Int, and `modBy (-4) 8` took the sign correction it should have
    // skipped and answered -4 instead of 0. Neither showed up until the NaN
    // reached an addition, frames or hours later, in a place with nothing to
    // do with modulo. Go and Swift compare against 0 and were always right.
    def('modBy', native(2, ([d, n]) => {
      if (d.n === 0) return VInt(0);
      let r = n.n % d.n;
      if (r !== 0 && (r < 0) !== (d.n < 0)) r += d.n;
      return VInt(r);
    }));
    def('remainderBy', native(2, ([d, n]) => (d.n === 0 ? VInt(0) : VInt(n.n % d.n))));

    // Tuple: 2-tuple helpers. Tuples are VTuple values with .xs.
    const tupleFirstImpl = native(1, ([t]) => t.xs[0]);
    def('tupleFirst', tupleFirstImpl);
    def('Tuple.first', tupleFirstImpl);

    const tupleSecondImpl = native(1, ([t]) => t.xs[1]);
    def('tupleSecond', tupleSecondImpl);
    def('Tuple.second', tupleSecondImpl);

    const tuplePairImpl = native(2, ([a, b]) => VTuple([a, b]));
    def('tuplePair', tuplePairImpl);
    def('Tuple.pair', tuplePairImpl);

    const tupleMapFirstImpl = native(2, ([fn, t]) =>
      VTuple([apply(fn, t.xs[0]), t.xs[1]]));
    def('tupleMapFirst', tupleMapFirstImpl);
    def('Tuple.mapFirst', tupleMapFirstImpl);

    const tupleMapSecondImpl = native(2, ([fn, t]) =>
      VTuple([t.xs[0], apply(fn, t.xs[1])]));
    def('tupleMapSecond', tupleMapSecondImpl);
    def('Tuple.mapSecond', tupleMapSecondImpl);

    const tupleMapBothImpl = native(3, ([fnA, fnB, t]) =>
      VTuple([apply(fnA, t.xs[0]), apply(fnB, t.xs[1])]));
    def('tupleMapBoth', tupleMapBothImpl);
    def('Tuple.mapBoth', tupleMapBothImpl);

    // ---------- Dict ----------
    //
    // Elm-style polymorphic ordered map (sorted by key). Same wire
    // format and same comparable-key constraint as the Go and Swift
    // runtimes. Pairs slice IS the canonical representation: every
    // mutation rebuilds it sorted via dictInsertHelper / etc.
    def('dictEmpty', VDict([]));
    def('Dict.empty', VDict([]));

    const dictSingletonImpl = native(2, ([k, v]) => VDict([{ key: k, value: v }]));
    def('dictSingleton', dictSingletonImpl);
    def('Dict.singleton', dictSingletonImpl);

    const dictInsertImpl = native(3, ([k, v, d]) => dictInsertHelper(d, k, v));
    def('dictInsert', dictInsertImpl);
    def('Dict.insert', dictInsertImpl);

    const dictUpdateImpl = native(3, ([k, fn, d]) => {
      const { idx, found } = dictSearch(d, k);
      const current = found
        ? VCtor('Just', [d.pairs[idx].value])
        : VCtor('Nothing');
      const next = apply(fn, current);
      if (next.tag === 'Nothing') {
        return found ? dictRemoveHelper(d, idx) : d;
      }
      if (next.tag === 'Just') {
        return dictPutAt(d, idx, found, k, next.args[0]);
      }
      throw new Error('Dict.update: function did not return a Maybe');
    });
    def('dictUpdate', dictUpdateImpl);
    def('Dict.update', dictUpdateImpl);

    const dictRemoveImpl = native(2, ([k, d]) => {
      const { idx, found } = dictSearch(d, k);
      return found ? dictRemoveHelper(d, idx) : d;
    });
    def('dictRemove', dictRemoveImpl);
    def('Dict.remove', dictRemoveImpl);

    const dictIsEmptyImpl = native(1, ([d]) => VBool(d.pairs.length === 0));
    def('dictIsEmpty', dictIsEmptyImpl);
    def('Dict.isEmpty', dictIsEmptyImpl);

    const dictMemberImpl = native(2, ([k, d]) => VBool(dictSearch(d, k).found));
    def('dictMember', dictMemberImpl);
    def('Dict.member', dictMemberImpl);

    const dictGetImpl = native(2, ([k, d]) => {
      const { idx, found } = dictSearch(d, k);
      return found ? VCtor('Just', [d.pairs[idx].value]) : VCtor('Nothing');
    });
    def('dictGet', dictGetImpl);
    def('Dict.get', dictGetImpl);

    const dictSizeImpl = native(1, ([d]) => VInt(d.pairs.length));
    def('dictSize', dictSizeImpl);
    def('Dict.size', dictSizeImpl);

    const dictKeysImpl = native(1, ([d]) => VList(d.pairs.map(p => p.key)));
    def('dictKeys', dictKeysImpl);
    def('Dict.keys', dictKeysImpl);

    const dictValuesImpl = native(1, ([d]) => VList(d.pairs.map(p => p.value)));
    def('dictValues', dictValuesImpl);
    def('Dict.values', dictValuesImpl);

    const dictToListImpl = native(1, ([d]) =>
      VList(d.pairs.map(p => VTuple([p.key, p.value]))));
    def('dictToList', dictToListImpl);
    def('Dict.toList', dictToListImpl);

    const dictFromListImpl = native(1, ([l]) =>
      dictFromPairs(l.xs.map(e => ({ key: e.xs[0], value: e.xs[1] }))));
    def('dictFromList', dictFromListImpl);
    def('Dict.fromList', dictFromListImpl);

    // Dict.map : (k -> v -> w) -> Dict k v -> Dict k w  - keys
    // untouched so we keep the pair order without re-sorting.
    const dictMapImpl = native(2, ([fn, d]) =>
      VDict(d.pairs.map(p => ({ key: p.key, value: applyMany(fn, [p.key, p.value]) }))));
    def('dictMap', dictMapImpl);
    def('Dict.map', dictMapImpl);

    const dictFoldlImpl = native(3, ([fn, init, d]) => {
      let acc = init;
      for (const p of d.pairs) acc = applyMany(fn, [p.key, p.value, acc]);
      return acc;
    });
    def('dictFoldl', dictFoldlImpl);
    def('Dict.foldl', dictFoldlImpl);

    const dictFoldrImpl = native(3, ([fn, init, d]) => {
      let acc = init;
      for (let i = d.pairs.length - 1; i >= 0; i--) {
        const p = d.pairs[i];
        acc = applyMany(fn, [p.key, p.value, acc]);
      }
      return acc;
    });
    def('dictFoldr', dictFoldrImpl);
    def('Dict.foldr', dictFoldrImpl);

    const dictFilterImpl = native(2, ([fn, d]) =>
      VDict(d.pairs.filter(p => applyMany(fn, [p.key, p.value]).b)));
    def('dictFilter', dictFilterImpl);
    def('Dict.filter', dictFilterImpl);

    const dictPartitionImpl = native(2, ([fn, d]) => {
      const yes = [], no = [];
      for (const p of d.pairs) {
        if (applyMany(fn, [p.key, p.value]).b) yes.push(p);
        else no.push(p);
      }
      return VTuple([VDict(yes), VDict(no)]);
    });
    def('dictPartition', dictPartitionImpl);
    def('Dict.partition', dictPartitionImpl);

    // Dict.union, left-biased: collision keeps `a`'s value.
    const dictUnionImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.pairs.length && j < b.pairs.length) {
        const c = cmpValues(a.pairs[i].key, b.pairs[j].key);
        if (c < 0) { out.push(a.pairs[i]); i++; }
        else if (c > 0) { out.push(b.pairs[j]); j++; }
        else { out.push(a.pairs[i]); i++; j++; }
      }
      while (i < a.pairs.length) out.push(a.pairs[i++]);
      while (j < b.pairs.length) out.push(b.pairs[j++]);
      return VDict(out);
    });
    def('dictUnion', dictUnionImpl);
    def('Dict.union', dictUnionImpl);

    const dictIntersectImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.pairs.length && j < b.pairs.length) {
        const c = cmpValues(a.pairs[i].key, b.pairs[j].key);
        if (c < 0) i++;
        else if (c > 0) j++;
        else { out.push(a.pairs[i]); i++; j++; }
      }
      return VDict(out);
    });
    def('dictIntersect', dictIntersectImpl);
    def('Dict.intersect', dictIntersectImpl);

    const dictDiffImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.pairs.length) {
        if (j >= b.pairs.length) { out.push(...a.pairs.slice(i)); break; }
        const c = cmpValues(a.pairs[i].key, b.pairs[j].key);
        if (c < 0) { out.push(a.pairs[i]); i++; }
        else if (c > 0) { j++; }
        else { i++; j++; }
      }
      return VDict(out);
    });
    def('dictDiff', dictDiffImpl);
    def('Dict.diff', dictDiffImpl);

    // ---------- Set ----------
    def('setEmpty', VSet([]));
    def('Set.empty', VSet([]));

    const setSingletonImpl = native(1, ([k]) => VSet([k]));
    def('setSingleton', setSingletonImpl);
    def('Set.singleton', setSingletonImpl);

    const setInsertImpl = native(2, ([k, s]) => setInsertHelper(s, k));
    def('setInsert', setInsertImpl);
    def('Set.insert', setInsertImpl);

    const setRemoveImpl = native(2, ([k, s]) => {
      const { idx, found } = setSearch(s, k);
      if (!found) return s;
      return VSet(arrayWithRemoved(s.items, idx));
    });
    def('setRemove', setRemoveImpl);
    def('Set.remove', setRemoveImpl);

    const setIsEmptyImpl = native(1, ([s]) => VBool(s.items.length === 0));
    def('setIsEmpty', setIsEmptyImpl);
    def('Set.isEmpty', setIsEmptyImpl);

    const setMemberImpl = native(2, ([k, s]) => VBool(setSearch(s, k).found));
    def('setMember', setMemberImpl);
    def('Set.member', setMemberImpl);

    const setSizeImpl = native(1, ([s]) => VInt(s.items.length));
    def('setSize', setSizeImpl);
    def('Set.size', setSizeImpl);

    const setToListImpl = native(1, ([s]) => VList(s.items.slice()));
    def('setToList', setToListImpl);
    def('Set.toList', setToListImpl);

    const setFromListImpl = native(1, ([l]) => setFromItems(l.xs.slice()));
    def('setFromList', setFromListImpl);
    def('Set.fromList', setFromListImpl);

    // Set.map can change the element type and the order, and can map two
    // items to one, so the mapped items are re-sorted and deduped rather
    // than copied in place.
    const setMapImpl = native(2, ([fn, s]) => setFromItems(s.items.map(it => apply(fn, it))));
    def('setMap', setMapImpl);
    def('Set.map', setMapImpl);

    const setFoldlImpl = native(3, ([fn, init, s]) => {
      let acc = init;
      for (const it of s.items) acc = applyMany(fn, [it, acc]);
      return acc;
    });
    def('setFoldl', setFoldlImpl);
    def('Set.foldl', setFoldlImpl);

    const setFoldrImpl = native(3, ([fn, init, s]) => {
      let acc = init;
      for (let i = s.items.length - 1; i >= 0; i--) acc = applyMany(fn, [s.items[i], acc]);
      return acc;
    });
    def('setFoldr', setFoldrImpl);
    def('Set.foldr', setFoldrImpl);

    const setFilterImpl = native(2, ([fn, s]) =>
      VSet(s.items.filter(it => apply(fn, it).b)));
    def('setFilter', setFilterImpl);
    def('Set.filter', setFilterImpl);

    const setPartitionImpl = native(2, ([fn, s]) => {
      const yes = [], no = [];
      for (const it of s.items) (apply(fn, it).b ? yes : no).push(it);
      return VTuple([VSet(yes), VSet(no)]);
    });
    def('setPartition', setPartitionImpl);
    def('Set.partition', setPartitionImpl);

    const setUnionImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.items.length && j < b.items.length) {
        const c = cmpValues(a.items[i], b.items[j]);
        if (c < 0) { out.push(a.items[i++]); }
        else if (c > 0) { out.push(b.items[j++]); }
        else { out.push(a.items[i++]); j++; }
      }
      while (i < a.items.length) out.push(a.items[i++]);
      while (j < b.items.length) out.push(b.items[j++]);
      return VSet(out);
    });
    def('setUnion', setUnionImpl);
    def('Set.union', setUnionImpl);

    const setIntersectImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.items.length && j < b.items.length) {
        const c = cmpValues(a.items[i], b.items[j]);
        if (c < 0) i++;
        else if (c > 0) j++;
        else { out.push(a.items[i++]); j++; }
      }
      return VSet(out);
    });
    def('setIntersect', setIntersectImpl);
    def('Set.intersect', setIntersectImpl);

    const setDiffImpl = native(2, ([a, b]) => {
      const out = [];
      let i = 0, j = 0;
      while (i < a.items.length) {
        if (j >= b.items.length) { out.push(...a.items.slice(i)); break; }
        const c = cmpValues(a.items[i], b.items[j]);
        if (c < 0) { out.push(a.items[i++]); }
        else if (c > 0) j++;
        else { i++; j++; }
      }
      return VSet(out);
    });
    def('setDiff', setDiffImpl);
    def('Set.diff', setDiffImpl);

    // ---------- Char ----------
    //
    // Char in Mar is a Unicode scalar value: same model as Go's rune,
    // Swift's Unicode.Scalar, Elm's Char. `c` is the integer scalar
    // value on VChar.
    //
    // sanitizeCodePoint mirrors the Go side: out-of-range or surrogate
    // inputs collapse to U+FFFD ("replacement character"), keeping the
    // three runtimes able to represent every Char.fromCode result.
    function sanitizeCodePoint(n) {
      if (n < 0 || n > 0x10FFFF) return 0xFFFD;
      if (n >= 0xD800 && n <= 0xDFFF) return 0xFFFD;
      return n;
    }

    const charToCodeImpl = native(1, ([c]) => VInt(c.c));
    def('charToCode', charToCodeImpl);
    def('Char.toCode', charToCodeImpl);

    const charFromCodeImpl = native(1, ([n]) => VChar(sanitizeCodePoint(n.n)));
    def('charFromCode', charFromCodeImpl);
    def('Char.fromCode', charFromCodeImpl);

    // Char predicates: operate on the Unicode properties of the
    // scalar. We use JS regex with the `u` flag so things like
    // `Char.isAlpha 'é'` work (and stay aligned with Go's unicode
    // package, which is also Unicode-aware not ASCII-only).
    const isDigitRe = /\p{Nd}/u;
    const isAlphaRe = /\p{L}/u;
    const isUpperRe = /\p{Lu}/u;
    const isLowerRe = /\p{Ll}/u;

    const charIsDigitImpl = native(1, ([c]) =>
      VBool(isDigitRe.test(String.fromCodePoint(c.c))));
    def('charIsDigit', charIsDigitImpl);
    def('Char.isDigit', charIsDigitImpl);

    const charIsAlphaImpl = native(1, ([c]) =>
      VBool(isAlphaRe.test(String.fromCodePoint(c.c))));
    def('charIsAlpha', charIsAlphaImpl);
    def('Char.isAlpha', charIsAlphaImpl);

    const charIsUpperImpl = native(1, ([c]) =>
      VBool(isUpperRe.test(String.fromCodePoint(c.c))));
    def('charIsUpper', charIsUpperImpl);
    def('Char.isUpper', charIsUpperImpl);

    const charIsLowerImpl = native(1, ([c]) =>
      VBool(isLowerRe.test(String.fromCodePoint(c.c))));
    def('charIsLower', charIsLowerImpl);
    def('Char.isLower', charIsLowerImpl);

    // Char.toUpper / Char.toLower are Unicode's SIMPLE case mappings, not the
    // full ones String.toUpper uses. A Char is one scalar (ADR 0038 clause 1)
    // and a full mapping can produce several: the full uppercase of the sharp
    // s is two letters, which a Char has nowhere to put.
    //
    // The rule that keeps the table small: wherever the full mapping is
    // exactly one scalar it IS the simple mapping. That holds across all 1.1M
    // scalars, so only the scalars whose full mapping is longer need listing,
    // and of those only the ones whose simple mapping is not the identity.
    // That is 27 (all Greek ypogegrammeni) plus one lowercase.
    // simple_case_test.go regenerates both tables from Go's own unicode data
    // and fails if Unicode moves under them.
    const SIMPLE_UPPER = {
      0x1F80: 0x1F88, 0x1F81: 0x1F89, 0x1F82: 0x1F8A, 0x1F83: 0x1F8B, 0x1F84: 0x1F8C, 0x1F85: 0x1F8D,
      0x1F86: 0x1F8E, 0x1F87: 0x1F8F, 0x1F90: 0x1F98, 0x1F91: 0x1F99, 0x1F92: 0x1F9A, 0x1F93: 0x1F9B,
      0x1F94: 0x1F9C, 0x1F95: 0x1F9D, 0x1F96: 0x1F9E, 0x1F97: 0x1F9F, 0x1FA0: 0x1FA8, 0x1FA1: 0x1FA9,
      0x1FA2: 0x1FAA, 0x1FA3: 0x1FAB, 0x1FA4: 0x1FAC, 0x1FA5: 0x1FAD, 0x1FA6: 0x1FAE, 0x1FA7: 0x1FAF,
      0x1FB3: 0x1FBC, 0x1FC3: 0x1FCC, 0x1FF3: 0x1FFC,
    };
    const SIMPLE_LOWER = { 0x0130: 0x0069, };
    const simpleCase = (code, mapped, table) => {
      const pts = [...mapped];
      if (pts.length === 1) return pts[0].codePointAt(0);
      const t = table[code];
      return t === undefined ? code : t;
    };

    const charToUpperImpl = native(1, ([c]) =>
      VChar(simpleCase(c.c, String.fromCodePoint(c.c).toUpperCase(), SIMPLE_UPPER)));
    def('charToUpper', charToUpperImpl);
    def('Char.toUpper', charToUpperImpl);

    const charToLowerImpl = native(1, ([c]) =>
      VChar(simpleCase(c.c, String.fromCodePoint(c.c).toLowerCase(), SIMPLE_LOWER)));
    def('charToLower', charToLowerImpl);
    def('Char.toLower', charToLowerImpl);

    // String <-> [Char] bridges. Iterating a JS string with for..of
    // (or spread) walks Unicode scalar values correctly (joins
    // surrogate pairs into one scalar). Same semantics as Go's
    // `for _, r := range s`.
    const stringToListImpl = native(1, ([s]) => {
      const out = [];
      for (const ch of s.s) out.push(VChar(ch.codePointAt(0)));
      return VList(out);
    });
    def('stringToList', stringToListImpl);
    def('String.toList', stringToListImpl);

    const stringFromListImpl = native(1, ([l]) => {
      let out = '';
      for (const c of l.xs) out += String.fromCodePoint(c.c);
      return VString(out);
    });
    def('stringFromList', stringFromListImpl);
    def('String.fromList', stringFromListImpl);

    const stringConsImpl = native(2, ([c, s]) =>
      VString(String.fromCodePoint(c.c) + s.s));
    def('stringCons', stringConsImpl);
    def('String.cons', stringConsImpl);

    // String higher-order ops over Char. Iterating with `for..of`
    // walks scalar values correctly (joins surrogate pairs into one
    // scalar). Matches the Go side's `for _, r := range s`.
    const stringMapImpl = native(2, ([fn, s]) => {
      let out = '';
      for (const ch of s.s) {
        const r = apply(fn, VChar(ch.codePointAt(0)));
        out += String.fromCodePoint(r.c);
      }
      return VString(out);
    });
    def('stringMap', stringMapImpl);
    def('String.map', stringMapImpl);

    const stringFilterImpl = native(2, ([fn, s]) => {
      let out = '';
      for (const ch of s.s) {
        if (apply(fn, VChar(ch.codePointAt(0))).b) out += ch;
      }
      return VString(out);
    });
    def('stringFilter', stringFilterImpl);
    def('String.filter', stringFilterImpl);

    // stringFoldl : (Char -> b -> b) -> b -> String -> b
    const stringFoldlImpl = native(3, ([fn, init, s]) => {
      let acc = init;
      for (const ch of s.s) {
        const c = VChar(ch.codePointAt(0));
        acc = applyMany(fn, [c, acc]);
      }
      return acc;
    });
    def('stringFoldl', stringFoldlImpl);
    def('String.foldl', stringFoldlImpl);

    // stringAny : (Char -> Bool) -> String -> Bool, short-circuit.
    const stringAnyImpl = native(2, ([fn, s]) => {
      for (const ch of s.s) {
        if (apply(fn, VChar(ch.codePointAt(0))).b) return VBool(true);
      }
      return VBool(false);
    });
    def('stringAny', stringAnyImpl);
    def('String.any', stringAnyImpl);

    // ---------- UI primitives: shared infrastructure ----------
    //
    // collectAttrs / makeAttr / flagAttr are used by every UI builtin
    // that takes an `[Attr]` list (containers, textField) and by the
    // input-kind / submit constants.

    function collectAttrs(attrsList) {
      const out = [];
      for (const a of attrsList.xs) {
        out.push({ name: a.fields.name.s, value: a.fields.value });
      }
      return out;
    }
    function makeAttr(name, value) {
      return VRecord({ name: VString(name), value }, ['name', 'value']);
    }
    function flagAttr(name) {
      return makeAttr(name, VUnit());
    }

    // submit / email / password / newPassword / numeric / oneTimeCode
    // are reached via UI.* qualified aliases (see the `UI.email` block
    // below). The bare runtime names start with `view` for historical
    // reasons; renaming them all would invalidate compiled program.json
    // wire payloads in flight. Keep as-is.
    const submitAttrCtor = native(1, ([msg]) => makeAttr('submit', msg));
    def('viewSubmit',       submitAttrCtor);
    def('viewEmail',        flagAttr('inputKindEmail'));
    def('viewPassword',     flagAttr('inputKindPassword'));
    def('viewNewPassword',  flagAttr('inputKindNewPassword'));
    def('viewNumeric',      flagAttr('inputKindNumeric'));
    def('viewOneTimeCode',  flagAttr('inputKindOneTimeCode'));

    // ---------- UI.* (SwiftUI-style declarative vocabulary) ----------
    //
    // Mirror of the Go runtime's UI builtins (internal/runtime/view.go).
    // Same VView / VAttr shapes, just a JS expression of the same
    // intermediate form. The renderer (createDOM further down) is the
    // authoritative interpreter; these builtins produce the data.

    // Containers
    function uiContainer(tag) {
      return native(2, ([attrsList, children]) =>
        VView(tag, collectAttrs(attrsList), children.xs, ''));
    }
    function uiContentOnly(tag) {
      return native(1, ([children]) =>
        VView(tag, [], children.xs, ''));
    }
    def('navigationStack', uiContainer('navigationStack'));
    def('UI.navigationStack', uiContainer('navigationStack'));
    def('form',  uiContentOnly('form'));   def('UI.form',    uiContentOnly('form'));
    def('list',  uiContainer('uiList'));   def('UI.list',    uiContainer('uiList'));
    def('uiSection', uiContainer('uiSection')); def('UI.section', uiContainer('uiSection'));
    def('uiKeyedList', uiContainer('uiKeyedList')); def('UI.keyedList', uiContainer('uiKeyedList'));
    def('hstack', uiContainer('hstack'));  def('UI.hstack',  uiContainer('hstack'));
    def('vstack', uiContainer('vstack'));  def('UI.vstack',  uiContainer('vstack'));

    // textField : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
    function uiTextField(args) {
      const [attrsList, placeholder, value, onChange] = args;
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'placeholder', value: placeholder });
      return VView('textField', attrs, [], value.s, onChange);
    }
    def('textField', native(4, uiTextField));
    def('UI.textField', native(4, uiTextField));

    // textArea : List Attr -> String placeholder -> String value -> (String -> msg) -> View msg
    // Multi-line text input. Same shape as textField: swapping
    // `textField` for `textArea` in user code is the only change
    // needed when the field needs to hold a paragraph instead of
    // a single line. iOS renders a TextEditor; web renders a
    // <textarea> with the same focus / submit / placeholder
    // styling so the two stack visually inside a section.
    //
    // Submit semantics on the web mirror SwiftUI: a bare Enter
    // inserts a newline (so users can write prose freely), and
    // Cmd/Ctrl+Enter fires the `submit` attr if one is attached.
    // The branching for that lives in attachSubmitDispatcher.
    function uiTextArea(args) {
      const [attrsList, placeholder, value, onChange] = args;
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'placeholder', value: placeholder });
      return VView('textArea', attrs, [], value.s, onChange);
    }
    def('textArea', native(4, uiTextArea));
    def('UI.textArea', native(4, uiTextArea));

    // picker : List Attr -> a -> List a -> (a -> String) -> (a -> msg) -> View msg
    // Single-selection field. Use when an enum has more than a
    // couple of variants and rendering one toggle per variant
    // would dominate the form's vertical real estate (priority,
    // assignee, milestone). iOS renders SwiftUI's Picker with the
    // platform's native menu / wheel; web renders a styled
    // <select>. `toLabel` is applied per option to produce the
    // displayed string: same shape user code already has for
    // status / priority badges (`Shared.priorityLabel`,
    // `Shared.statusLabel`).
    //
    // The selected value is identified structurally (eqValues),
    // so callers pass any ctor / int / string / record: whatever
    // the option list contains.
    function uiPicker(args) {
      const [attrsList, selected, options, toLabel, onChange] = args;
      const attrs = collectAttrs(attrsList);
      // Stash the selected value, the option list, and the label
      // function as attrs so createDOM/patchDOM can rebuild the
      // <select> from `view.attrs` alone. msg carries onChange so
      // the dispatcher path mirrors textField / toggle (apply
      // view.msg to the selected option).
      attrs.push({ name: 'selected', value: selected });
      attrs.push({ name: 'options',  value: options });
      attrs.push({ name: 'toLabel',  value: toLabel });
      return VView('picker', attrs, [], '', onChange);
    }
    def('picker', native(5, uiPicker));
    def('UI.picker', native(5, uiPicker));

    // datePicker : List Attr -> Date -> (Date -> msg) -> View msg
    // Date-only field. Stash the Date as the `value` attr so createDOM /
    // patchDOM can render the <input type="date"> from view.attrs alone;
    // msg carries onChange, applied to the Date of the picked day.
    function uiDatePicker(args) {
      const [attrsList, value, onChange] = args;
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'value', value: value });
      return VView('datePicker', attrs, [], '', onChange);
    }
    def('datePicker', native(3, uiDatePicker));
    def('UI.datePicker', native(3, uiDatePicker));

    // --- Canvas (v0.0.7): the 2D draw-list ---
    // canvas : CanvasMode -> List (Attr Canvas) -> List Shape -> View msg
    // The Shape children ride in `children` as raw Shape VCtors (not
    // VViews); the createDOM 'canvas' case paints them onto a <canvas>
    // sized to its own box and reports onTap / watchSize through the attrs.
    // arg 0 is the mandatory render mode (Pixelated | Crisp); it rides as a
    // synthetic `canvasMode` attr that drawCanvas reads to size the buffer.
    const canvasCtor = native(3, ([mode, attrsList, shapes]) => {
      const m = (mode && mode.tag === 'Crisp') ? 'crisp' : 'pixelated';
      const attrs = collectAttrs(attrsList);
      attrs.unshift({ name: 'canvasMode', value: VString(m) });
      const xs = (shapes && shapes.k === 'L' && shapes.xs) ? shapes.xs : [];
      return VView('canvas', attrs, xs, '', null);
    });
    def('canvas', canvasCtor); def('Canvas.canvas', canvasCtor);
    // Shape / Color builders: pure data (VCtor), drawn by the renderer.
    const rectCtor     = native(5, args => VCtor('rect', args.slice()));
    const circleCtor   = native(4, args => VCtor('circle', args.slice()));
    const triangleCtor = native(7, args => VCtor('triangle', args.slice()));
    const textCtor     = native(6, args => VCtor('text', args.slice()));
    const rgbCtor      = native(3, args => VCtor('rgb', args.slice()));
    const rgbaCtor     = native(4, args => VCtor('rgba', args.slice()));
    const groupCtor    = native(2, ([transforms, shapes]) => VCtor('group', [transforms, shapes]));
    def('rect', rectCtor);         def('Canvas.rect', rectCtor);
    def('circle', circleCtor);     def('Canvas.circle', circleCtor);
    def('triangle', triangleCtor); def('Canvas.triangle', triangleCtor);
    def('canvasText', textCtor);   def('Canvas.text', textCtor);
    def('rgb', rgbCtor);         def('Canvas.rgb', rgbCtor);
    def('rgba', rgbaCtor);       def('Canvas.rgba', rgbaCtor);
    def('group', groupCtor);     def('Canvas.group', groupCtor);
    const onTapCtor         = native(1, args => makeAttr('onTap', args[0]));
    const watchSizeCtor     = native(1, args => makeAttr('watchSize', args[0]));
    const watchPointersCtor = native(1, args => makeAttr('watchPointers', args[0]));
    const onReleaseCtor     = native(1, args => makeAttr('onRelease', args[0]));
    const onDragCtor        = native(1, args => makeAttr('onDrag', args[0]));
    const onHoverCtor       = native(1, args => makeAttr('onHover', args[0]));
    const onAltTapCtor      = native(1, args => makeAttr('onAltTap', args[0]));
    const onWheelCtor       = native(1, args => makeAttr('onWheel', args[0]));
    def('onTap', onTapCtor);                 def('Canvas.onTap', onTapCtor);
    def('watchSize', watchSizeCtor);         def('Canvas.watchSize', watchSizeCtor);
    def('watchPointers', watchPointersCtor); def('Canvas.watchPointers', watchPointersCtor);
    def('onRelease', onReleaseCtor);         def('Canvas.onRelease', onReleaseCtor);
    def('onDrag', onDragCtor);               def('Canvas.onDrag', onDragCtor);
    def('onHover', onHoverCtor);             def('Canvas.onHover', onHoverCtor);
    def('onAltTap', onAltTapCtor);           def('Canvas.onAltTap', onAltTapCtor);
    def('onWheel', onWheelCtor);             def('Canvas.onWheel', onWheelCtor);
    // BEGIN GENERATED BUILTIN CTORS (go generate ./internal/ctorgen), DO NOT EDIT
    // Every qualified builtin union constructor, straight from the
    // typecheck tables (CustomType.Module). Nothing here is hand-picked:
    // if a name is missing, fix the union in typecheck and regenerate.
    def('Canvas.Left', VCtor('Left'));
    def('Canvas.Center', VCtor('Center'));
    def('Canvas.Right', VCtor('Right'));
    def('Auth.CodeSent', VCtor('CodeSent'));
    def('Auth.InvalidEmail', VCtor('InvalidEmail'));
    def('Auth.RateLimited', VCtor('RateLimited'));
    def('Auth.SignedIn', native(1, args => VCtor('SignedIn', args.slice())));
    def('Auth.WrongCode', VCtor('WrongCode'));
    def('Auth.TooManyAttempts', VCtor('TooManyAttempts'));
    def('Canvas.Normal', VCtor('Normal'));
    def('Canvas.Add', VCtor('Add'));
    def('Canvas.Multiply', VCtor('Multiply'));
    def('Canvas.Screen', VCtor('Screen'));
    def('Canvas.Erase', VCtor('Erase'));
    def('Gamepad.A', VCtor('A'));
    def('Gamepad.B', VCtor('B'));
    def('Gamepad.X', VCtor('X'));
    def('Gamepad.Y', VCtor('Y'));
    def('Gamepad.L1', VCtor('L1'));
    def('Gamepad.R1', VCtor('R1'));
    def('Gamepad.L2', VCtor('L2'));
    def('Gamepad.R2', VCtor('R2'));
    def('Gamepad.Select', VCtor('Select'));
    def('Gamepad.Start', VCtor('Start'));
    def('Gamepad.L3', VCtor('L3'));
    def('Gamepad.R3', VCtor('R3'));
    def('Gamepad.Up', VCtor('Up'));
    def('Gamepad.Down', VCtor('Down'));
    def('Gamepad.Left', VCtor('Left'));
    def('Gamepad.Right', VCtor('Right'));
    def('Keyboard.KeyA', VCtor('KeyA'));
    def('Keyboard.KeyB', VCtor('KeyB'));
    def('Keyboard.KeyC', VCtor('KeyC'));
    def('Keyboard.KeyD', VCtor('KeyD'));
    def('Keyboard.KeyE', VCtor('KeyE'));
    def('Keyboard.KeyF', VCtor('KeyF'));
    def('Keyboard.KeyG', VCtor('KeyG'));
    def('Keyboard.KeyH', VCtor('KeyH'));
    def('Keyboard.KeyI', VCtor('KeyI'));
    def('Keyboard.KeyJ', VCtor('KeyJ'));
    def('Keyboard.KeyK', VCtor('KeyK'));
    def('Keyboard.KeyL', VCtor('KeyL'));
    def('Keyboard.KeyM', VCtor('KeyM'));
    def('Keyboard.KeyN', VCtor('KeyN'));
    def('Keyboard.KeyO', VCtor('KeyO'));
    def('Keyboard.KeyP', VCtor('KeyP'));
    def('Keyboard.KeyQ', VCtor('KeyQ'));
    def('Keyboard.KeyR', VCtor('KeyR'));
    def('Keyboard.KeyS', VCtor('KeyS'));
    def('Keyboard.KeyT', VCtor('KeyT'));
    def('Keyboard.KeyU', VCtor('KeyU'));
    def('Keyboard.KeyV', VCtor('KeyV'));
    def('Keyboard.KeyW', VCtor('KeyW'));
    def('Keyboard.KeyX', VCtor('KeyX'));
    def('Keyboard.KeyY', VCtor('KeyY'));
    def('Keyboard.KeyZ', VCtor('KeyZ'));
    def('Keyboard.Digit0', VCtor('Digit0'));
    def('Keyboard.Digit1', VCtor('Digit1'));
    def('Keyboard.Digit2', VCtor('Digit2'));
    def('Keyboard.Digit3', VCtor('Digit3'));
    def('Keyboard.Digit4', VCtor('Digit4'));
    def('Keyboard.Digit5', VCtor('Digit5'));
    def('Keyboard.Digit6', VCtor('Digit6'));
    def('Keyboard.Digit7', VCtor('Digit7'));
    def('Keyboard.Digit8', VCtor('Digit8'));
    def('Keyboard.Digit9', VCtor('Digit9'));
    def('Keyboard.F1', VCtor('F1'));
    def('Keyboard.F2', VCtor('F2'));
    def('Keyboard.F3', VCtor('F3'));
    def('Keyboard.F4', VCtor('F4'));
    def('Keyboard.F5', VCtor('F5'));
    def('Keyboard.F6', VCtor('F6'));
    def('Keyboard.F7', VCtor('F7'));
    def('Keyboard.F8', VCtor('F8'));
    def('Keyboard.F9', VCtor('F9'));
    def('Keyboard.F10', VCtor('F10'));
    def('Keyboard.F11', VCtor('F11'));
    def('Keyboard.F12', VCtor('F12'));
    def('Keyboard.ArrowUp', VCtor('ArrowUp'));
    def('Keyboard.ArrowDown', VCtor('ArrowDown'));
    def('Keyboard.ArrowLeft', VCtor('ArrowLeft'));
    def('Keyboard.ArrowRight', VCtor('ArrowRight'));
    def('Keyboard.Space', VCtor('Space'));
    def('Keyboard.Enter', VCtor('Enter'));
    def('Keyboard.Tab', VCtor('Tab'));
    def('Keyboard.Backspace', VCtor('Backspace'));
    def('Keyboard.Escape', VCtor('Escape'));
    def('Keyboard.Delete', VCtor('Delete'));
    def('Keyboard.Insert', VCtor('Insert'));
    def('Keyboard.Home', VCtor('Home'));
    def('Keyboard.End', VCtor('End'));
    def('Keyboard.PageUp', VCtor('PageUp'));
    def('Keyboard.PageDown', VCtor('PageDown'));
    def('Keyboard.ShiftLeft', VCtor('ShiftLeft'));
    def('Keyboard.ShiftRight', VCtor('ShiftRight'));
    def('Keyboard.ControlLeft', VCtor('ControlLeft'));
    def('Keyboard.ControlRight', VCtor('ControlRight'));
    def('Keyboard.AltLeft', VCtor('AltLeft'));
    def('Keyboard.AltRight', VCtor('AltRight'));
    def('Keyboard.MetaLeft', VCtor('MetaLeft'));
    def('Keyboard.MetaRight', VCtor('MetaRight'));
    def('Keyboard.CapsLock', VCtor('CapsLock'));
    def('Keyboard.NumLock', VCtor('NumLock'));
    def('Keyboard.ScrollLock', VCtor('ScrollLock'));
    def('Keyboard.PrintScreen', VCtor('PrintScreen'));
    def('Keyboard.Pause', VCtor('Pause'));
    def('Keyboard.ContextMenu', VCtor('ContextMenu'));
    def('Keyboard.Backquote', VCtor('Backquote'));
    def('Keyboard.Minus', VCtor('Minus'));
    def('Keyboard.Equal', VCtor('Equal'));
    def('Keyboard.BracketLeft', VCtor('BracketLeft'));
    def('Keyboard.BracketRight', VCtor('BracketRight'));
    def('Keyboard.Backslash', VCtor('Backslash'));
    def('Keyboard.Semicolon', VCtor('Semicolon'));
    def('Keyboard.Quote', VCtor('Quote'));
    def('Keyboard.Comma', VCtor('Comma'));
    def('Keyboard.Period', VCtor('Period'));
    def('Keyboard.Slash', VCtor('Slash'));
    def('Keyboard.Numpad0', VCtor('Numpad0'));
    def('Keyboard.Numpad1', VCtor('Numpad1'));
    def('Keyboard.Numpad2', VCtor('Numpad2'));
    def('Keyboard.Numpad3', VCtor('Numpad3'));
    def('Keyboard.Numpad4', VCtor('Numpad4'));
    def('Keyboard.Numpad5', VCtor('Numpad5'));
    def('Keyboard.Numpad6', VCtor('Numpad6'));
    def('Keyboard.Numpad7', VCtor('Numpad7'));
    def('Keyboard.Numpad8', VCtor('Numpad8'));
    def('Keyboard.Numpad9', VCtor('Numpad9'));
    def('Keyboard.NumpadAdd', VCtor('NumpadAdd'));
    def('Keyboard.NumpadSubtract', VCtor('NumpadSubtract'));
    def('Keyboard.NumpadMultiply', VCtor('NumpadMultiply'));
    def('Keyboard.NumpadDivide', VCtor('NumpadDivide'));
    def('Keyboard.NumpadDecimal', VCtor('NumpadDecimal'));
    def('Keyboard.NumpadEnter', VCtor('NumpadEnter'));
    def('Decimal.HalfEven', VCtor('HalfEven'));
    def('Decimal.HalfUp', VCtor('HalfUp'));
    def('Decimal.Down', VCtor('Down'));
    def('Decimal.Up', VCtor('Up'));
    def('Decimal.Floor', VCtor('Floor'));
    def('Decimal.Ceiling', VCtor('Ceiling'));
    def('Service.Offline', VCtor('Offline'));
    def('Service.Unauthorized', VCtor('Unauthorized'));
    def('Service.RateLimited', VCtor('RateLimited'));
    def('Service.ServerError', native(1, args => VCtor('ServerError', args.slice())));
    def('Sound.Square', VCtor('Square'));
    def('Sound.Triangle', VCtor('Triangle'));
    def('Sound.Sawtooth', VCtor('Sawtooth'));
    def('Sound.Sine', VCtor('Sine'));
    def('Sound.Noise', VCtor('Noise'));
    def('Canvas.Translate', native(2, args => VCtor('Translate', args.slice())));
    def('Canvas.Scale', native(2, args => VCtor('Scale', args.slice())));
    def('Canvas.Rotate', native(1, args => VCtor('Rotate', args.slice())));
    def('Canvas.Alpha', native(1, args => VCtor('Alpha', args.slice())));
    def('Canvas.Blend', native(1, args => VCtor('Blend', args.slice())));
    // END GENERATED BUILTIN CTORS

    // text: plain text leaf. The attrs list carries the universal
    // layout attrs (width / height); `text [width fill] "..."` is
    // the equal-columns idiom.
    const uiTextCtor = native(2, ([attrsList, s]) =>
      VView('text', collectAttrs(attrsList), [], s.s));
    def('uiText', uiTextCtor); def('UI.text', uiTextCtor);

    // paragraph : List (Inline msg) -> View msg
    // Block of flowing inline text. Children are `span` VViews
    // produced by uiSpan; renderer flows them into one <p>.
    const uiParagraphCtor = native(1, ([children]) =>
      VView('paragraph', [], children.xs, ''));
    def('uiParagraph', uiParagraphCtor); def('UI.paragraph', uiParagraphCtor);

    // span : List (Attr Inline) -> String -> Inline msg
    // Inline text run with styling attrs (bold/italic/code/link
    // composing freely).
    const uiSpanCtor = native(2, ([attrsList, s]) =>
      VView('span', collectAttrs(attrsList), [], s.s));
    def('uiSpan', uiSpanCtor); def('UI.span', uiSpanCtor);

    // Inline attrs. Bare style markers (bold/italic/strikethrough/
    // code) carry no payload; the renderer checks attr name to
    // toggle the corresponding CSS class. `link` is the one
    // parameterized inline attr: its payload is the destination URL.
    const inlineBoldAttr          = flagAttr('inlineBold');
    const inlineItalicAttr        = flagAttr('inlineItalic');
    const inlineStrikethroughAttr = flagAttr('inlineStrikethrough');
    const inlineCodeAttr          = flagAttr('inlineCode');
    def('inlineBold', inlineBoldAttr);          def('UI.bold', inlineBoldAttr);
    def('inlineItalic', inlineItalicAttr);      def('UI.italic', inlineItalicAttr);
    def('inlineStrikethrough', inlineStrikethroughAttr);
    def('UI.strikethrough', inlineStrikethroughAttr);
    def('inlineCode', inlineCodeAttr);          def('UI.code', inlineCodeAttr);
    const inlineLinkCtor = native(1, ([url]) => makeAttr('inlineLink', url));
    def('inlineLink', inlineLinkCtor);          def('UI.link', inlineLinkCtor);

    // button : List Attr -> msg -> String -> View msg
    // Attrs carry modifiers like `disabled` that the renderer reads
    // to grey-out the button and skip dispatch.
    const uiButtonCtor = native(3, ([attrsList, msg, label]) =>
      VView('button', collectAttrs(attrsList), [], label.s, msg));
    def('uiButton', uiButtonCtor); def('UI.button', uiButtonCtor);

    // disabled : Bool -> Attr, kept symmetric for both true/false so
    // user code can pass a derived Bool without conditionally building
    // the attrs list.
    const uiDisabledCtor = native(1, ([b]) => makeAttr('disabled', b));
    def('uiDisabled', uiDisabledCtor); def('UI.disabled', uiDisabledCtor);

    // keyed : String -> View msg -> KeyedView msg
    // Wraps a regular View in a stable identity (the key string) so
    // it can be a child of UI.keyedList. The KeyedView distinction
    // is compile-time only: at runtime we just append a `key` attr
    // to the inner VView. The reconciler reads that attr when
    // matching old/new children inside a keyedList.
    //
    // Returns a shallow copy with attrs extended (not the same VView
    // mutated) so callers that hold a reference to the unwrapped
    // view don\'t see their attrs grow as a side effect.
    const uiKeyedCtor = native(2, ([keyStr, view]) => {
      const attrs = view.attrs.slice();
      attrs.push({ name: 'key', value: keyStr });
      return VView(view.tag, attrs, view.children, view.text, view.msg);
    });
    def('uiKeyed', uiKeyedCtor); def('UI.keyed', uiKeyedCtor);

    // onMove : Bool -> (Int -> Int -> msg) -> Attr KeyedList
    // Reorder gesture handler for `list`. The Bool toggles whether
    // drag affordances render (handle + tabindex + ARIA wiring);
    // the function fires with (fromIdx, toIdx) once the user drops.
    //
    // Packs both into a single record value on the attr so the
    // renderer's `list` handler can pull editing + handler off in
    // one place. Storing them separately would make it easier to
    // forget one when wiring the drag listeners.
    const uiOnMoveCtor = native(2, ([editingV, handler]) => {
      const editing = editingV && editingV.b === true;
      return makeAttr('onMove', { editing, handler });
    });
    def('uiOnMove', uiOnMoveCtor); def('UI.onMove', uiOnMoveCtor);

    // onDelete : Bool -> (Int -> msg) -> Attr Section
    // Per-row delete affordance. Bool = "editing mode is currently
    // on"; in that state, every row shows a permanent red `−` on
    // the left (iOS-edit-mode style). When false, the affordance
    // reveals on hover instead (iCloud-Web style: see
    // docs/cli-surface-proposal.md for the design rationale).
    //
    // The handler receives the index of the deleted row. The app is
    // expected to remove that index from its model (and typically
    // fire a Service.call to persist), then optionally surface a
    // toast with an Undo affordance.
    //
    // Same packing shape as onMove so renderers can pick both off in
    // one place.
    const uiOnDeleteCtor = native(2, ([editingV, handler]) => {
      const editing = editingV && editingV.b === true;
      return makeAttr('onDelete', { editing, handler });
    });
    def('uiOnDelete', uiOnDeleteCtor); def('UI.onDelete', uiOnDeleteCtor);

    // title / subtitle: reuse the existing "title" / "subtitle"
    // tags so createDOM's existing handling applies; UI styles
    // (font-size / weight / color) live in ensureUIStyles().
    const uiTitleCtor = native(1, ([s]) => VView('title', [], [], s.s));
    def('uiTitle', uiTitleCtor); def('UI.title', uiTitleCtor);
    const uiSubtitleCtor = native(1, ([s]) => VView('subtitle', [], [], s.s));
    def('uiSubtitle', uiSubtitleCtor); def('UI.subtitle', uiSubtitleCtor);

    // errorText: same leaf shape as text/title/subtitle. Tag
    // 'errorText' triggers the .mar-error-text CSS class in
    // createDOM (red + semi-bold) and a role=alert for assistive
    // tech. Mirrors Go's runtime/view.go uiErrorText + iOS's
    // MarRenderer "errorText" case.
    const uiErrorTextCtor = native(1, ([s]) => VView('errorText', [], [], s.s));
    def('uiErrorText', uiErrorTextCtor); def('UI.errorText', uiErrorTextCtor);

    // image : List (Attr Image) -> { src, alt } -> View msg
    // Emits an "image" tag carrying src + alt (and any size/fit/fill
    // attrs) so createDOM can build an <img>. Mirrors Go runtime's
    // uiImage + iOS MarRenderer "image" case.
    const uiImageCtor = native(2, ([attrsList, rec]) => {
      const attrs = collectAttrs(attrsList);
      const src = rec.fields.src ? rec.fields.src.s : '';
      const alt = rec.fields.alt ? rec.fields.alt.s : '';
      attrs.push({ name: 'src', value: VString(src) });
      attrs.push({ name: 'alt', value: VString(alt) });
      return VView('image', attrs, [], '');
    });
    def('uiImage', uiImageCtor); def('UI.image', uiImageCtor);

    // chars / lines / fill: sizing values. chars/lines wrap an Int
    // in a record tagged with __unit so the renderer can dispatch on
    // what the number means (horizontal characters vs vertical
    // lines); fill is the axis-polymorphic "take the available
    // space" constant (same record shape, amount unused). Mirrors Go
    // runtime's lengthValue helper.
    const uiCharsCtor = native(1, ([n]) =>
      VRecord({ __unit: VString('chars'), amount: VInt(n.n) }, ['__unit', 'amount']));
    def('uiChars', uiCharsCtor); def('UI.chars', uiCharsCtor);
    const uiLinesCtor = native(1, ([n]) =>
      VRecord({ __unit: VString('lines'), amount: VInt(n.n) }, ['__unit', 'amount']));
    def('uiLines', uiLinesCtor); def('UI.lines', uiLinesCtor);
    const uiFillVal =
      VRecord({ __unit: VString('fill'), amount: VInt(0) }, ['__unit', 'amount']);
    def('uiFill', uiFillVal); def('UI.fill', uiFillVal);

    // width / height: the universal sizing attrs. applyLayoutAttrs
    // reads the Size record's __unit: chars/lines size the content
    // box (inputs keep their special-cased max-width / rows
    // handling in applySizing), fill claims the free space on that
    // axis via the contextual .mar-w-fill / .mar-h-fill classes.
    const uiWidthCtor  = native(1, ([v]) => makeAttr('width',  v));
    def('uiWidth',  uiWidthCtor);  def('UI.width',  uiWidthCtor);
    const uiHeightCtor = native(1, ([v]) => makeAttr('height', v));
    def('uiHeight', uiHeightCtor); def('UI.height', uiHeightCtor);

    // align: cross-axis alignment for a stack's hugging children.
    // The value is a plain alignment-name string; applyAlignAttr
    // maps it onto align-items, honoring only the axis that matches
    // the stack (vstack: leading/center/trailing; hstack:
    // top/center/bottom). Children with the matching `fill` have no
    // cross-axis slack, so align never moves them: align is
    // position, fill is size.
    const uiAlignCtor = native(1, ([v]) => makeAttr('align', v));
    def('uiAlign', uiAlignCtor); def('UI.align', uiAlignCtor);
    def('uiLeading', VString('leading'));   def('UI.leading', VString('leading'));
    def('uiCenter', VString('center'));     def('UI.center', VString('center'));
    def('uiTrailing', VString('trailing')); def('UI.trailing', VString('trailing'));
    def('uiTop', VString('top'));           def('UI.top', VString('top'));
    def('uiBottom', VString('bottom'));     def('UI.bottom', VString('bottom'));

    // px: pixel sizing unit for images (mirrors chars/lines, tagged
    // 'px'). size, fixed width+height attr for an image. fit/cover:
    // content-mode flags (CSS object-fit vocabulary; "cover", not
    // "fill", which is the sizing value above). createDOM's 'image'
    // case reads these.
    const uiPxCtor = native(1, ([n]) =>
      VRecord({ __unit: VString('px'), amount: VInt(n.n) }, ['__unit', 'amount']));
    def('uiPx', uiPxCtor); def('UI.px', uiPxCtor);
    const uiSizeCtor = native(2, ([w, h]) =>
      makeAttr('size', VRecord({ w, h }, ['w', 'h'])));
    def('uiSize', uiSizeCtor); def('UI.size', uiSizeCtor);
    const fitAttr = flagAttr('contentModeFit');
    def('uiFit', fitAttr); def('UI.fit', fitAttr);
    const coverAttr = flagAttr('contentModeCover');
    def('uiCover', coverAttr); def('UI.cover', coverAttr);

    // navigationLink : List Attr -> Path r -> r -> View msg -> View msg
    // Mirror of SwiftUI's NavigationLink. The Path + record build
    // the destination URL via the typed-path machinery; the
    // child View becomes the tappable label. Renders as
    // <a class="mar-navigation-link"> wrapping the child DOM.
    // The leading attrs list carries `disabled` (and future
    // modifiers): uniform shape with every other interactive
    // primitive.
    def('uiNavigationLink', native(4, ([attrsList, pathV, params, child]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('UI.navigationLink: expected Path, got ' + (pathV && pathV.k));
      }
      if (!child || child.k !== 'V') {
        throw new Error('UI.navigationLink: expected View label, got ' + (child && child.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'href', value: VString(url) });
      return VView('navigationLink', attrs, [child], '');
    }));
    def('UI.navigationLink', envLookup(env, 'uiNavigationLink'));

    // empty: no-op placeholder; same VView the existing renderer
    // already knows to handle (display: none).
    def('uiEmpty', VView('empty', [], [], ''));
    def('UI.empty', VView('empty', [], [], ''));

    // spacer: SwiftUI's `Spacer()`. Expands along the containing
    // stack's main axis to push siblings apart. On web that's a
    // `flex: 1` div the parent flex container absorbs.
    def('uiSpacer', VView('spacer', [], [], ''));
    def('UI.spacer', VView('spacer', [], [], ''));

    // toggle : List Attr -> String -> Bool -> (Bool -> msg) -> View msg
    // Mirror of SwiftUI's Toggle(label, isOn: $value). Current
    // state lives in the `isOn` attr, label in text, the
    // `Bool -> msg` callback in msg. createDOM renders a label
    // wrapping an iOS-style styled checkbox; on change the
    // checkbox dispatches `msg(newValue)`. The leading attrs
    // list carries modifiers like `disabled`: same shape every
    // other interactive primitive uses (textField / button /
    // picker), so the gating idiom is uniform.
    def('uiToggle', native(4, ([attrsList, label, isOn, onChange]) => {
      const attrs = collectAttrs(attrsList);
      attrs.push({ name: 'isOn', value: VBool(!!isOn.b) });
      return VView('toggle', attrs, [], label.s, onChange);
    }));
    def('UI.toggle', envLookup(env, 'uiToggle'));

    // centered : View msg -> View msg
    // Wraps child in a "centered" view tag: pure two-axis
    // alignment. The renderer fills the space the PARENT provides
    // (never inventing a size) and centers the child in it.
    const uiCenteredCtor = native(1, ([child]) =>
      VView('centered', [], [child], ''));
    def('uiCentered', uiCenteredCtor); def('UI.centered', uiCenteredCtor);

    // sheet : { open, onDismiss, outlet } -> List (View msg) -> View msg
    //
    // iOS-style page sheet. Parent owns open/closed state; framework
    // renders the overlay + animation + history glue. See the
    // createDOM/patchDOM case 'sheet' for the rendering logic and the
    // `.mar-sheet-*` CSS rules for the visual treatment.
    const uiSheetCtor = native(2, ([config, children]) => {
      const open = config.fields.open;       // VBool
      const outlet = config.fields.outlet;   // VString
      const onDismiss = config.fields.onDismiss; // any Msg value
      return VView('sheet', [
        { name: 'open',   value: open },
        { name: 'outlet', value: outlet },
      ], children.xs, '', onDismiss);
    });
    def('uiSheet', uiSheetCtor); def('UI.sheet', uiSheetCtor);

    // confirm : { title, confirmLabel, destructive, onConfirm,
    //             onCancel } -> View msg
    //
    // Destructive-action confirmation dialog. Render-time semantics
    // is "if this view appears in the tree, mount the modal; if it
    // doesn't, the modal is gone." Apps therefore branch via `case`
    // returning `UI.confirm {...}` when active and `UI.empty` when
    // not: there's no explicit `isOpen` field.
    //
    // We stash both message handlers as attrs because the view has
    // two distinct dispatch paths (confirm + cancel) and VView's
    // single `msg` slot can only hold one. Renderer reads both off
    // attrs when wiring the dialog buttons + backdrop tap.
    const uiConfirmCtor = native(1, ([config]) => {
      return VView('confirmDialog', [
        { name: 'title',        value: config.fields.title },
        { name: 'confirmLabel', value: config.fields.confirmLabel },
        { name: 'destructive',  value: config.fields.destructive },
        { name: 'onConfirm',    value: config.fields.onConfirm },
        { name: 'onCancel',     value: config.fields.onCancel },
      ], [], '', null);
    });
    def('uiConfirm', uiConfirmCtor); def('UI.confirm', uiConfirmCtor);

    // Modifier attrs.
    const navTitleCtor   = native(1, ([s]) => makeAttr('navigationTitle', s));
    def('navigationTitle', navTitleCtor); def('UI.navigationTitle', navTitleCtor);
    // topBarTrailing / topBarLeading: toolbar items at the trailing
    // / leading edge of the top bar. Names match SwiftUI's
    // `.topBarTrailing` / `.topBarLeading` placement (iOS 17+).
    const topBarTrailingCtor = native(1, ([v]) => makeAttr('topBarTrailing', v));
    def('uiTopBarTrailing', topBarTrailingCtor);
    def('UI.topBarTrailing', topBarTrailingCtor);
    const topBarLeadingCtor  = native(1, ([v]) => makeAttr('topBarLeading', v));
    def('uiTopBarLeading',  topBarLeadingCtor);
    def('UI.topBarLeading',  topBarLeadingCtor);
    const headerCtor     = native(1, ([s]) => makeAttr('header', s));
    def('header', headerCtor); def('UI.header', headerCtor);
    const footerCtor     = native(1, ([s]) => makeAttr('footer', s));
    def('footer', footerCtor); def('UI.footer', footerCtor);

    // numericCode bundles numeric keypad + Code-from-Mail autofill:
    // single flag attr; the renderer expands it (see applyInputKind).
    const numericCodeAttr = flagAttr('inputKindNumericCode');
    def('numericCode', numericCodeAttr); def('UI.numericCode', numericCodeAttr);

    // Re-expose existing input-kind / submit attrs under UI.* so user
    // code that lives in the SwiftUI-style vocabulary stays in one
    // namespace. Same runtime values.
    def('UI.email',       flagAttr('inputKindEmail'));
    def('UI.password',    flagAttr('inputKindPassword'));
    def('UI.newPassword', flagAttr('inputKindNewPassword'));
    def('UI.numeric',     flagAttr('inputKindNumeric'));
    def('UI.oneTimeCode', flagAttr('inputKindOneTimeCode'));
    def('UI.submit',      submitAttrCtor);

    // Page.create takes a record { path, title?, init, update, view }.
    // Produces a VCtor "__Page" with positional args the mountPages
    // code consumes. `title` is optional; when omitted the browser's
    // existing tab title stays.
    const pageCreateImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__Page', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageCreate', pageCreateImpl);
    def('Page.create', pageCreateImpl);

    // Page.protected: same shape as Page.create plus User-aware
    // handler signatures (init/update/view receive the logged-in
    // User as first arg). The mountPages code recognizes the
    // "__ProtectedPage" tag, runs Auth.me on first entry, and
    // either threads the User into handlers or navigates to the
    // sign-in path declared in Auth.config.signInPage.
    const pageProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__ProtectedPage', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageProtected', pageProtectedImpl);
    def('Page.protected', pageProtectedImpl);

    // Page.adminProtected mints the opaque AdminSession capability and threads
    // it into init/update/view as the leading argument. The AdminSession is a
    // placeholder: the real authorization is the admin session cookie, which
    // the server checks on every /_mar/admin/api/mar/* route.
    //
    // The gate below is UX, not a boundary, and the distinction is load-bearing
    // (ADR 0031). It cannot be a boundary: the page's view, update and every
    // literal in them are already in the program.json that any visitor can GET,
    // so nothing a client-side check does can un-send them. What it buys is
    // that an operator who is not signed in lands on the panel's login screen
    // instead of watching an ops page render and then fail — the same courtesy
    // Page.protected does for users. Anything genuinely secret must come from
    // a service that checks the session itself.
    //
    // It rides as a property rather than a tag for the reason Page.sheet does:
    // it composes with the plain __Page shape the pre-application produces.
    // (Web-only; no iOS admin app.)
    const pageAdminProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      const adminSession = VString('admin');
      // init : AdminSession -> (Model, Effect), pre-applying the
      // session yields the (model, effect) tuple a plain page's init
      // now IS (no vestigial unit arg). The tuple is pure data and
      // the effect inside is a lazy description, so evaluating here
      // at page-construction time is equivalent to first mount.
      const init = apply(f.init, adminSession);
      const update = native(2, ([msg, model]) => apply(apply(apply(f.update, adminSession), msg), model));
      const view = native(1, ([model]) => apply(apply(f.view, adminSession), model));
      const subscriptions = native(1, ([model]) => apply(apply(f.subscriptions, adminSession), model));
      const p = VCtor('__Page', [f.path, init, update, view, title, subscriptions]);
      p.adminOnly = true;
      return p;
    });
    def('pageAdminProtected', pageAdminProtectedImpl);
    def('Page.adminProtected', pageAdminProtectedImpl);

    // Mar.Admin.*: privileged server-introspection, shaped like Service.call
    // (AdminSession -> (Result String resp -> msg) -> Cmd msg). The
    // panel performs them as Cmds; the result arrives through toMsg. The
    // AdminSession argument is the compile-time gate only: at runtime the
    // admin session cookie (same-origin) authorizes the request and the server
    // runs the real introspection body (internal/jsserve/admin_mar.go),
    // returning the Mar Value as JSON which jsToMar rebuilds here.
    const marAdminFetch = (path, toMsg) => VEffect(() => {
      const issuer = effectIssuer;
      fetch(path, { method: 'GET', credentials: 'same-origin' })
        .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
        .then(r => {
          if (r.status === 401) {
            // Admin session missing/expired: send the operator to the panel's
            // own sign-in page. Guarded so the panel's several concurrent
            // fetches redirect once.
            if (!globalThis.__marAdminRedirecting) {
              globalThis.__marAdminRedirecting = true;
              window.location.assign('/_mar/admin/login');
            }
            return;
          }
          if (!r.ok) {
            deliverFrom(issuer, apply(toMsg, VCtor('Err', [VString(decodeServerError(r.body) || ('HTTP ' + r.status))])));
            return;
          }
          let parsed;
          try { parsed = jsToMar(jsonBodyOrThrow(r.body)); }
          catch (e) {
            deliverFrom(issuer, apply(toMsg, VCtor('Err', [VString('decode failed: ' + (e && e.message || e))])));
            return;
          }
          deliverFrom(issuer, apply(toMsg, VCtor('Ok', [parsed])));
        })
        .catch(err => {
          deliverFrom(issuer, apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))])));
        });
      return VUnit();
    }, 'marAdmin');

    def('marAdminServerInfo', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/server-info', toMsg)));
    def('Mar.Admin.serverInfo', envLookup(env, 'marAdminServerInfo'));
    def('marAdminDbStats', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/db-stats', toMsg)));
    def('Mar.Admin.dbStats', envLookup(env, 'marAdminDbStats'));
    def('marAdminRecentRequests', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/recent-requests', toMsg)));
    def('Mar.Admin.recentRequests', envLookup(env, 'marAdminRecentRequests'));
    def('marAdminListEntities', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/entities', toMsg)));
    def('Mar.Admin.listEntities', envLookup(env, 'marAdminListEntities'));
    def('marAdminListEntityRows', native(3, ([_s, entity, toMsg]) => marAdminFetch('/_mar/admin/api/mar/entity-rows?entity=' + encodeURIComponent(entity.s), toMsg)));
    def('Mar.Admin.listEntityRows', envLookup(env, 'marAdminListEntityRows'));
    def('marAdminListBackups', native(2, ([_s, toMsg]) => marAdminFetch('/_mar/admin/api/mar/backups', toMsg)));
    def('Mar.Admin.listBackups', envLookup(env, 'marAdminListBackups'));
    // Admin sign-in flow: POST to the existing /_mar/admin/auth/* endpoints
    // (reuses authPost, same as the user Auth.*). Pre-auth, so no AdminSession.
    def('marAdminRequestCode', native(2, ([req, toMsg]) => authPost('/_mar/admin/auth/request-code', marToJs(req), toMsg, () => VUnit())));
    def('Mar.Admin.requestCode', envLookup(env, 'marAdminRequestCode'));
    def('marAdminVerifyCode', native(2, ([req, toMsg]) => authPost('/_mar/admin/auth/verify-code', marToJs(req), toMsg, (text) => jsToMar(JSON.parse(text)))));
    def('Mar.Admin.verifyCode', envLookup(env, 'marAdminVerifyCode'));
    def('marAdminSignOut', native(1, ([toMsg]) => authPost('/_mar/admin/auth/logout', null, toMsg, () => VUnit())));
    def('Mar.Admin.signOut', envLookup(env, 'marAdminSignOut'));

    // Page.dynamic, pattern path with `:param` segments. The runtime
    // matches the URL against the pattern at navigation time, threading
    // a Params record through init/update/view as the leading argument.
    // The wire-format ctor is __DynamicPage; mountPages parses the path
    // pattern lazily so the framework only walks segments once per page.
    const pageDynamicImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__DynamicPage', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageDynamic', pageDynamicImpl);
    def('Page.dynamic', pageDynamicImpl);

    // Page.dynamicProtected: pattern path + auth gate. Combines
    // __DynamicPage's URL matching with __ProtectedPage's Auth.me
    // bootstrap. Handler signature is `User -> Params -> ...` (User
    // first, mirroring Page.protected).
    const pageDynamicProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      return VCtor('__DynamicProtectedPage', [f.path, f.init, f.update, f.view, title, f.subscriptions]);
    });
    def('pageDynamicProtected', pageDynamicProtectedImpl);
    def('Page.dynamicProtected', pageDynamicProtectedImpl);

    // Page.sheet: presentation, not a fifth kind of page. Takes a page
    // built by any constructor above and marks it PRESENTED: navigating
    // to it leaves the page you came from on screen and lays this one
    // over it in a sheet. Route, history entry and deep link are
    // unchanged; only the painting differs (see mountPages/render).
    //
    // Carried as a property on the ctor value rather than a seventh
    // positional arg, so every existing reader that destructures
    // [path, init, update, view, title, subscriptions] keeps working
    // untouched.
    // A copy with one more flag. Every flag the page already carries rides
    // along (Page.adminProtected's adminOnly among them), so wrapping a page
    // in Page.sheet changes where it is drawn and nothing else.
    const pageSheetImpl = native(1, ([p]) => {
      if (!p || p.k !== 'C') return p;
      return Object.assign({}, p, { presented: true });
    });
    def('pageSheet', pageSheetImpl);
    def('Page.sheet', pageSheetImpl);

    // Page.dynamicAdminProtected: pattern path + admin session. Pre-applies a
    // placeholder AdminSession (the admin cookie does the real auth on the
    // Mar.Admin.* fetches) and emits a plain __DynamicPage, so the existing
    // dynamic-page machinery threads Params in. Web-only (no iOS admin).
    const pageDynamicAdminProtectedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const title = f.title || VString('');
      const adminSession = VString('admin');
      // Real sigs thread (AdminSession, Params, …); pre-apply the session,
      // leaving (Params, …): exactly __DynamicPage's shape.
      const init = native(1, ([params]) => apply(apply(f.init, adminSession), params));
      const update = native(3, ([params, msg, model]) => apply(apply(apply(apply(f.update, adminSession), params), msg), model));
      const view = native(2, ([params, model]) => apply(apply(apply(f.view, adminSession), params), model));
      const subscriptions = native(2, ([params, model]) => apply(apply(apply(f.subscriptions, adminSession), params), model));
      return VCtor('__DynamicPage', [f.path, init, update, view, title, subscriptions]);
    });
    def('pageDynamicAdminProtected', pageDynamicAdminProtectedImpl);
    def('Page.dynamicAdminProtected', pageDynamicAdminProtectedImpl);

    // Nav.*: programmatic navigation. The actual implementations
    // live inside mountPages (where `pages`/`render` are in scope) and
    // get attached to globalThis.__marNav at mount time. Calling them
    // before mountPages has run is a no-op (effect runs but has nothing
    // to navigate within).
    def('navPush', native(1, ([url]) => VEffect(() => {
      const nav = globalThis.__marNav;
      if (nav) nav.push(url.s);
      return VUnit();
    }, 'navPush')));
    def('Nav.push', envLookup(env, 'navPush'));

    def('navReplace', native(1, ([url]) => VEffect(() => {
      const nav = globalThis.__marNav;
      if (nav) nav.replace(url.s);
      return VUnit();
    }, 'navReplace')));
    def('Nav.replace', envLookup(env, 'navReplace'));

    // Nav.dismiss : Cmd msg, close a route that is being presented
    // (Page.sheet). A VALUE, not a function: it takes nothing.
    //
    // Goes through history, exactly like the backdrop / Escape / Back
    // dismissals, so the URL and what is on screen stay one fact. With
    // nothing presented it steps back one entry inside the app; at the
    // app's first entry it does nothing, so a sheet route opened cold
    // (full-screen, nothing behind it) cannot walk the reader off the
    // site.
    def('navDismiss', VEffect(() => {
      if (refusedFromCovered('Nav.dismiss')) return VUnit();
      if (globalThis.__marPresentedActive && globalThis.__marPresentedActive()) {
        globalThis.__marDismissPresented();
      } else if (currentNavDepth() > 0) {
        history.back();
      }
      return VUnit();
    }, 'navDismiss'));
    def('Nav.dismiss', envLookup(env, 'navDismiss'));

    // Auth.completeSignIn : Cmd ()
    //
    // The right way to navigate after a successful Auth.verifyCode.
    // Reads the `?next=` query parameter (set by the framework when a
    // 401 redirected the user here, or when they followed a deep link
    // while unauthed) and goes there. Falls back to "/" when next is
    // absent, malformed, or fails the same-origin path-only safety
    // check (open-redirect prevention).
    //
    // User code:
    //   SubmitDone (Ok ()) ->
    //       ( model, Auth.completeSignIn )
    //
    // Lives under Auth.* (not Nav.*) because it bundles auth-specific
    // cleanup (resetting the auth-expired redirect coalescer) with
    // the navigation step. Nav.* stays focused on pure navigation;
    // Auth.* owns the post-login transition end-to-end.
    def('authCompleteSignIn', VEffect(() => {
      const nav = globalThis.__marNav;
      if (!nav) return VUnit();
      let target = '/';
      try {
        const params = new URLSearchParams(window.location.search);
        const next = params.get('next');
        if (next && isSafeReturnPath(next)) {
          target = next;
        }
      } catch (_) {
        // Defensive: URLSearchParams is universally supported but
        // some test environments may not have window.location.
      }
      // Reset the auth-expired coalescer so the next genuine session
      // expiry triggers a fresh redirect. Has to happen BEFORE the
      // nav call because the post-replace render may itself fire a
      // Service.call that would race against a stale flag.
      globalThis.__marRedirectingToSignIn = false;
      // replaceFresh (not replace) so the back button on the
      // destination doesn't point at /sign-in/verify/<email>. The
      // auth flow is one-way: once signed in, going "back" to the
      // (now-irrelevant) code-entry screen would only confuse the
      // user. Resetting depth to 0 reflects that this is a fresh
      // starting point.
      nav.replaceFresh(target);
      return VUnit();
    }, 'authCompleteSignIn'));
    def('Auth.completeSignIn', envLookup(env, 'authCompleteSignIn'));

    // isSafeReturnPath validates that a `?next=` value is a same-
    // origin relative path with no protocol-injection escape hatches.
    // Rejects:
    //   - protocol URLs:    "https://evil.com/x", "javascript:alert(1)"
    //   - protocol-relative "//evil.com" (browsers treat as cross-origin)
    //   - path traversal:   "/\\..", "/.."
    //   - missing leading /: "evil.com/x"
    function isSafeReturnPath(p) {
      if (typeof p !== 'string' || p.length === 0) return false;
      if (!p.startsWith('/')) return false;
      if (p.startsWith('//') || p.startsWith('/\\')) return false;
      if (/^\/+\.\.(\/|$)/.test(p)) return false;
      // Reject anything that looks like a protocol scheme. The leading
      // / blocks the simple case but a chain like "/x/javascript:..."
      // could be misinterpreted by a buggy router; we just don't
      // accept any colon before the first slash after the prefix.
      // Conservative check: refuse colons in the first path segment.
      const firstSegEnd = p.indexOf('/', 1);
      const firstSeg = firstSegEnd < 0 ? p.slice(1) : p.slice(1, firstSegEnd);
      if (firstSeg.indexOf(':') >= 0) return false;
      return true;
    }

    // Cache parsed Path patterns by source string. Keeps linkTo and
    // navPushTo / navReplaceTo cheap when the same Path value is used
    // many times (e.g. across a list render).
    const pathCache = Object.create(null);
    function parsePathCached(src) {
      let p = pathCache[src];
      if (!p) {
        p = parsePathPattern(src);
        pathCache[src] = p;
      }
      return p;
    }

    // linkTo : Path r -> r -> String
    // Build a URL from a typed Path + the params record. Path values
    // are VStrings at runtime (the typechecker enforces the surface
    // type); the cached parsePathPattern turns them into typed segments
    // for the URL builder. Used in `href` attributes: pure, no Effect.
    def('linkTo', native(2, ([pathV, params]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('linkTo: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      return VString(buildPathURL(pattern, params));
    }));

    // Nav.pushTo : Path r -> r -> Cmd msg
    // Type-safe sibling of Nav.push: pre-renders the URL via the
    // typed Path, then reuses the same global navigation hook. The
    // URL build runs eagerly (so missing-param errors surface
    // synchronously), but the actual history mutation only fires
    // when the Effect's Run is invoked by the runtime.
    def('navPushTo', native(2, ([pathV, params]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('Nav.pushTo: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      return VEffect(() => {
        const nav = globalThis.__marNav;
        if (nav) nav.push(url);
        return VUnit();
      }, 'navPushTo');
    }));
    def('Nav.pushTo', envLookup(env, 'navPushTo'));

    def('navReplaceTo', native(2, ([pathV, params]) => {
      if (!pathV || pathV.k !== 'S') {
        throw new Error('Nav.replaceTo: expected Path, got ' + (pathV && pathV.k));
      }
      const pattern = parsePathCached(pathV.s);
      const url = buildPathURL(pattern, params);
      return VEffect(() => {
        const nav = globalThis.__marNav;
        if (nav) nav.replace(url);
        return VUnit();
      }, 'navReplaceTo');
    }));
    def('Nav.replaceTo', envLookup(env, 'navReplaceTo'));

    // App.locale : String
    //
    // The tag the shell wrote into <html lang>, which the server took
    // from mar.json. Read from the document rather than carried in
    // program.json: the attribute is already there, it is already the
    // thing browsers and screen readers act on, and an old bundle
    // therefore needs no new field to keep working. Empty only if a
    // host page dropped the attribute, in which case "en" matches what
    // the Go side falls back to.
    const localeTag = (typeof document !== 'undefined'
      && document.documentElement
      && document.documentElement.lang) || 'en';
    def('appLocale', VString(localeTag));
    def('App.locale', VString(localeTag));

    // App.frontend : List Page -> Cmd ()
    // Mounts a page list with URL routing. Port comes from the host
    // server's mar.json, not user code.
    def('appFrontend', native(1, ([list]) => VEffect(() => mountPages(list.xs), 'mountPages')));
    def('App.frontend', native(1, ([list]) => VEffect(() => mountPages(list.xs), 'mountPages')));

    // App.shared : { init, update, subscriptions } -> App.Shared model msg
    //
    // Builds the DEF, not the store. The store is created on first use (see
    // sharedStoreFor) and keyed by this value's identity, which is why the
    // def has to be a top-level binding: it must evaluate once so every use
    // site holds the same object.
    //
    // __originKey is the hot-reload identity: the def OBJECT dies on reload,
    // the declaration order doesn't.
    const appSharedImpl = native(1, ([rec]) => {
      const f = rec.fields;
      const d = VCtor('__Shared', [f.init, f.update, f.subscriptions]);
      d.__originKey = 'shared:' + (sharedDefSeq++);
      return d;
    });
    def('appShared', appSharedImpl);
    def('App.shared', appSharedImpl);

    // Page.withShared : App.Shared model msg -> (model -> Page) -> Page
    //
    // Carries the builder rather than calling it: mountPages re-applies it
    // whenever the shared model changes, which is what makes the page render
    // the LIVE value. Calling it here would freeze the model at boot.
    const pageWithSharedImpl = native(2, ([sharedDef, builder]) =>
      VCtor('__SharedPage', [sharedDef, builder])
    );
    def('pageWithShared', pageWithSharedImpl);
    def('Page.withShared', pageWithSharedImpl);

    // Cmd.toShared : App.Shared model msg -> msg -> Cmd pageMsg
    //
    // An ordinary Cmd, so it batches with Cmd.batch and composes with
    // Cmd.perform for free. It resolves against the store, never against the
    // page, so a message issued before a navigation still lands after it.
    const cmdToSharedImpl = native(2, ([sharedDef, msg]) =>
      VEffect(() => { sharedDispatch(sharedDef, msg); return VUnit(); }, 'toShared')
    );
    def('cmdToShared', cmdToSharedImpl);
    def('Cmd.toShared', cmdToSharedImpl);

    // App.backend : List Route -> Task ()
    // Backend is a server-side concept. The browser bundle never sees
    // backend routes; this builtin returns a no-op Effect on the JS side.
    def('appBackend', native(1, ([_]) => VEffect(() => VUnit(), 'noop')));
    def('App.backend', native(1, ([_]) => VEffect(() => VUnit(), 'noop')));

    // App.fullstack : { api, pages } -> Cmd ()
    // The browser only cares about `pages`; `api` runs server-side.
    def('appFullstack', native(1, ([rec]) =>
      VEffect(() => mountPages(rec.fields.pages.xs), 'mountPages')
    ));
    def('App.fullstack', native(1, ([rec]) =>
      VEffect(() => mountPages(rec.fields.pages.xs), 'mountPages')
    ));

    // Effect: sync versions (effects are run-on-demand thunks).
    def('effectSucceed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('Task.succeed', native(1, ([v]) => VEffect(() => v, 'pure')));
    def('effectMap', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('Task.map', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()), 'map')));
    def('effectAndThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));
    def('Task.andThen', native(2, ([fn, eff]) => VEffect(() => apply(fn, eff.run()).run(), 'andThen')));

    // Task.fail : e -> Task a, throws when run. Uncaught
    // failures surface as a JS exception in the dispatcher; user
    // code that wants typed recovery should use Result.* instead.
    const effectFailImpl = native(1, ([err]) => VEffect(() => {
      const msg = err && err.k === 'S' ? err.s : ('(' + (err && err.k) + ')');
      throw new Error('Task.fail: ' + msg);
    }, 'fail'));
    def('effectFail', effectFailImpl);
    def('Task.fail', effectFailImpl);

    // Task.forEach : (a -> Task ()) -> List a -> Task ()
    // Sequential: halts on first failure (the thrown error
    // propagates out of run()).
    const effectForEachImpl = native(2, ([fn, list]) => VEffect(() => {
      for (const x of list.xs) {
        const eff = apply(fn, x);
        eff.run();
      }
      return VUnit();
    }, 'forEach'));
    def('effectForEach', effectForEachImpl);
    def('Task.forEach', effectForEachImpl);

    // Task.sequence : List (Task a) -> Task (List a)
    const effectSequenceImpl = native(1, ([list]) => VEffect(() => {
      const out = new Array(list.xs.length);
      for (let i = 0; i < list.xs.length; i++) {
        out[i] = list.xs[i].run();
      }
      return VList(out);
    }, 'sequence'));
    def('effectSequence', effectSequenceImpl);
    def('Task.sequence', effectSequenceImpl);

    // Cmd.batch : List (Cmd msg) -> Cmd msg
    // Fire-and-forget fan-out (the Cmd.batch of Mar). Each child's
    // run() kicks off its own work and delivers through its own
    // toMsg dispatch (Service.call effects self-dispatch when their
    // fetch resolves), so the children genuinely run concurrently.
    // The batch's own value is unit, same dynamic shape as
    // Effect.none.
    const effectBatchImpl = native(1, ([list]) => VEffect(() => {
      for (const e of list.xs) {
        if (e && e.k === 'E' && typeof e.run === 'function') e.run();
      }
      return VUnit();
    }, 'batch'));
    def('effectBatch', effectBatchImpl);
    def('Cmd.batch', effectBatchImpl);

    def('effectNone', VEffect(() => VUnit(), 'none'));
    def('Cmd.none', VEffect(() => VUnit(), 'none'));

    // Sub.none / Sub.batch: the frontend subscription monoid. A Sub is a
    // declarative descriptor (k:'SUB'); the IIFE-level reconcileSubs reads it.
    const subNoneVal = { k: 'SUB', items: [] };
    def('subNone', subNoneVal);
    def('Sub.none', subNoneVal);
    const subBatchImpl = native(1, ([list]) => {
      const items = [];
      const xs = (list && list.xs) || [];
      for (const sub of xs) {
        if (sub && sub.k === 'SUB' && sub.items) {
          for (const it of sub.items) items.push(it);
        }
      }
      return { k: 'SUB', items };
    });
    def('subBatch', subBatchImpl);
    def('Sub.batch', subBatchImpl);

    // Random: Elm-style generators with a PURE, seedable core. A Generator a
    // is Seed -> (a, Seed): native(1) taking a Seed, returning VTuple([value,
    // nextSeed]). PCG-XSH-RR mirrors internal/runtime/random.go bit-for-bit:
    // the golden vectors in random_test.go are the contract. A Seed rides
    // inside a VTuple of two 32-bit halves, opaque at the type level, so it
    // serializes/compares like any tuple and needs no new kind.
    //
    // The 64-bit state is worked on as those same two unsigned 32-bit halves,
    // in plain Numbers. It used to be one BigInt, and every step then built
    // about ten heap BigInts: a third of a Random.list's time went to
    // collecting them. Everything below is exact: the one 32x32 product that
    // needs its high half is split into 16-bit pieces, so no intermediate
    // passes 2^53.
    const PCG_MUL_HI = 0x5851F42D, PCG_MUL_LO = 0x4C957F2D; // 6364136223846793005
    const PCG_INC_HI = 0x14057B7E, PCG_INC_LO = 0xF767814F; // 1442695040888963407
    // pcgStep returns the XSH-RR output of the state (hi, lo) and leaves the
    // next state, state * MUL + INC mod 2^64, in pcgHi / pcgLo. Read them
    // before the next step.
    let pcgHi = 0, pcgLo = 0;
    const pcgStep = (hi, lo) => {
      // rotr32(((state >> 18) ^ state) >> 27, state >> 59)
      const xhi = (hi ^ (hi >>> 18)) >>> 0;
      const xlo = (lo ^ ((lo >>> 18) | (hi << 14))) >>> 0;
      const xs = ((xlo >>> 27) | (xhi << 5)) >>> 0;
      const rot = hi >>> 27;
      const out = ((xs >>> rot) | (xs << (32 - rot))) >>> 0;
      // lo * MUL_LO in full (64 bits), from 16-bit pieces
      const a0 = lo & 0xFFFF, a1 = lo >>> 16;
      const b0 = PCG_MUL_LO & 0xFFFF, b1 = PCG_MUL_LO >>> 16;
      const p00 = a0 * b0, p01 = a0 * b1, p10 = a1 * b0;
      const mid = (p00 >>> 16) + (p01 & 0xFFFF) + (p10 & 0xFFFF);
      const prodLo = (mid & 0xFFFF) * 0x10000 + (p00 & 0xFFFF);
      // high half: the carry out of lo * MUL_LO, plus the two cross terms,
      // whose own high halves fall off the top of 64 bits
      const prodHi = a1 * b1 + (p01 >>> 16) + (p10 >>> 16) + (mid >>> 16) +
        Math.imul(lo, PCG_MUL_HI) + Math.imul(hi, PCG_MUL_LO);
      const sumLo = prodLo + PCG_INC_LO;
      pcgLo = sumLo >>> 0;
      pcgHi = (prodHi + PCG_INC_HI + (sumLo > 0xFFFFFFFF ? 1 : 0)) >>> 0;
      return out;
    };
    const makeSeed = (hi, lo) => VTuple([VInt(hi), VInt(lo)]);
    // One step from a Seed: returns the output; the next Seed's halves are
    // in pcgHi / pcgLo.
    const stepSeed = (seed) => pcgStep(seed.xs[0].n >>> 0, seed.xs[1].n >>> 0);
    // Random.initialSeed n: n as an unsigned 64-bit value (two's complement),
    // advanced twice.
    const initialSeed = (n) => {
      pcgStep(Math.floor(n / 4294967296) >>> 0, n >>> 0);
      pcgStep(pcgHi, pcgLo);
      return makeSeed(pcgHi, pcgLo);
    };
    const entropySeed = () => {
      const a = new Uint32Array(2);
      (globalThis.crypto || (typeof require !== 'undefined' && require('crypto').webcrypto)).getRandomValues(a);
      return makeSeed(a[0], a[1]);
    };
    const runGen = (g, seed) => { const r = apply(g, seed); return [r.xs[0], r.xs[1]]; };
    const asGen = (step) => native(1, ([seed]) => { const [v, next] = step(seed); return VTuple([v, next]); });

    // Random.initialSeed : Int -> Seed
    const randomInitialSeed = native(1, ([n]) => initialSeed(n.n));
    def('randomInitialSeed', randomInitialSeed); def('Random.initialSeed', randomInitialSeed);
    // Random.step : Generator a -> Seed -> (a, Seed), pure, runs anywhere.
    const randomStep = native(2, ([g, seed]) => { const [v, next] = runGen(g, seed); return VTuple([v, next]); });
    def('randomStep', randomStep); def('Random.step', randomStep);
    // Random.seed : Task Seed, real OS entropy as a Seed.
    const randomSeed = VEffect(() => entropySeed(), 'randomSeed');
    def('randomSeed', randomSeed); def('Random.seed', randomSeed);

    // Random.generate : (a -> msg) -> Generator a -> Cmd msg, seeds from
    // entropy, steps once, dispatches the value as a Msg.
    const randomGenerate = native(2, ([toMsg, g]) => VEffect(() => {
      const issuer = effectIssuer;
      const [v] = runGen(g, entropySeed());
      deliverFrom(issuer, apply(toMsg, v));
      return VUnit();
    }, 'randomGenerate'));
    def('randomGenerate', randomGenerate); def('Random.generate', randomGenerate);
    const randomInt = native(2, ([lo, hi]) => {
      let a = lo.n, b = hi.n; if (a > b) { const t = a; a = b; b = t; }
      const span = b - a + 1;
      return asGen((seed) => { const out = stepSeed(seed); return [VInt(a + (out % span)), makeSeed(pcgHi, pcgLo)]; });
    });
    def('randomInt', randomInt); def('Random.int', randomInt);
    const randomConstant = native(1, ([v]) => asGen((seed) => [v, seed]));
    def('randomConstant', randomConstant); def('Random.constant', randomConstant);
    const randomUniform = native(2, ([first, rest]) => {
      const items = [first].concat((rest && rest.xs) || []);
      return asGen((seed) => { const out = stepSeed(seed); return [items[out % items.length], makeSeed(pcgHi, pcgLo)]; });
    });
    def('randomUniform', randomUniform); def('Random.uniform', randomUniform);
    const randomList = native(2, ([n, g]) => asGen((seed) => {
      const count = Math.max(0, n.n); const out = []; let cur = seed;
      for (let i = 0; i < count; i++) { const [v, next] = runGen(g, cur); out.push(v); cur = next; }
      return [VList(out), cur];
    }));
    def('randomList', randomList); def('Random.list', randomList);
    const randomPair = native(2, ([g1, g2]) => asGen((seed) => {
      const [v1, s1] = runGen(g1, seed); const [v2, s2] = runGen(g2, s1);
      return [VTuple([v1, v2]), s2];
    }));
    def('randomPair', randomPair); def('Random.pair', randomPair);
    const randomMap = native(2, ([f, g]) => asGen((seed) => { const [v, next] = runGen(g, seed); return [apply(f, v), next]; }));
    def('randomMap', randomMap); def('Random.map', randomMap);
    const randomMap2 = native(3, ([f, g1, g2]) => asGen((seed) => {
      const [v1, s1] = runGen(g1, seed); const [v2, s2] = runGen(g2, s1);
      return [applyMany(f, [v1, v2]), s2];
    }));
    def('randomMap2', randomMap2); def('Random.map2', randomMap2);
    const randomMap3 = native(4, ([f, g1, g2, g3]) => asGen((seed) => {
      const [v1, s1] = runGen(g1, seed); const [v2, s2] = runGen(g2, s1); const [v3, s3] = runGen(g3, s2);
      return [applyMany(f, [v1, v2, v3]), s3];
    }));
    def('randomMap3', randomMap3); def('Random.map3', randomMap3);
    const randomAndThen = native(2, ([f, g]) => asGen((seed) => {
      const [v, s1] = runGen(g, seed); return runGen(apply(f, v), s1);
    }));
    def('randomAndThen', randomAndThen); def('Random.andThen', randomAndThen);

    // Cmd.perform : (a -> msg) -> Task a -> Cmd msg
    // The Task->Cmd bridge (Elm's Task.perform): run the task and deliver
    // its produced value to the MVU loop as a msg. The only way a Task
    // (Time.now, future client reads) reaches `update` on the frontend.
    const cmdPerformImpl = native(2, ([toMsg, task]) => VEffect(() => {
      const issuer = effectIssuer;
      const v = task.run();
      deliverFrom(issuer, apply(toMsg, v));
      return VUnit();
    }, 'perform'));
    def('cmdPerform', cmdPerformImpl);
    def('Cmd.perform', cmdPerformImpl);

    // Math: Angle constructors, angle algebra, and the four functions.
    // Mirrors internal/runtime/math.go; the kernels themselves live in the
    // "Math (integer trigonometry)" section above, next to the generated
    // table. Each constructor reduces into one turn BEFORE scaling, so
    // `Math.degrees` of a huge Int cannot overflow on the way to a value
    // that was always going to land in 0..3599.
    const mkAngle = (perTurn, scale) =>
      native(1, ([n]) => VAngle(posMod(n.n, perTurn) * scale));
    const degreesImpl = mkAngle(360, 10);
    const deciDegreesImpl = mkAngle(DECI_FULL, 1);
    // turns counts in brads: 256 to the turn, the unit seasons-gp and
    // vortex already use. 3600/256 is not whole, so one brad floors; a
    // full turn is exact.
    const turnsImpl = native(1, ([n]) =>
      VAngle(Math.floor(posMod(n.n, 256) * DECI_FULL / 256)));
    def('mathDegrees', degreesImpl);         def('Math.degrees', degreesImpl);
    def('mathDeciDegrees', deciDegreesImpl); def('Math.deciDegrees', deciDegreesImpl);
    def('mathTurns', turnsImpl);             def('Math.turns', turnsImpl);
    const angleAdd = native(2, ([a, b]) => VAngle(posMod(a.deci + b.deci, DECI_FULL)));
    const angleSub = native(2, ([a, b]) => VAngle(posMod(a.deci - b.deci, DECI_FULL)));
    const angleOpp = native(1, ([a]) => VAngle(posMod(a.deci + 1800, DECI_FULL)));
    def('mathAdd', angleAdd);           def('Math.add', angleAdd);
    def('mathSubtract', angleSub);      def('Math.subtract', angleSub);
    def('mathOpposite', angleOpp);      def('Math.opposite', angleOpp);
    const sinImpl = native(1, ([a]) => VInt(sinDeci(a.deci)));
    const cosImpl = native(1, ([a]) => VInt(cosDeci(a.deci)));
    const atan2Impl = native(2, ([y, x]) => VAngle(atan2Deci(y.n, x.n)));
    const isqrtImpl = native(1, ([n]) => VInt(isqrtInt(n.n)));
    def('mathSin', sinImpl);            def('Math.sin', sinImpl);
    def('mathCos', cosImpl);            def('Math.cos', cosImpl);
    def('mathAtan2', atan2Impl);        def('Math.atan2', atan2Impl);
    def('mathIsqrt', isqrtImpl);        def('Math.isqrt', isqrtImpl);

    // Time: Duration-typed unit smart constructors, as the Go runtime's
    // timeBuiltins (internal/runtime/time.go). Each is n of its unit in
    // milliseconds, exactly, or a refusal when that is past Int.
    const mkDuration = (name, unitMs) => native(1, ([n]) => {
      const ms = n.n * unitMs;
      if (!Number.isSafeInteger(ms)) throw durationOutOfRange(name + ' ' + n.n);
      return VDuration(ms);
    });
    const timeMillisImpl = mkDuration('Time.millis', 1);
    def('timeMillis', timeMillisImpl);    def('Time.millis', timeMillisImpl);
    const timeSecondsImpl = mkDuration('Time.seconds', 1000);
    def('timeSeconds', timeSecondsImpl);  def('Time.seconds', timeSecondsImpl);
    const timeMinutesImpl = mkDuration('Time.minutes', 60 * 1000);
    def('timeMinutes', timeMinutesImpl);  def('Time.minutes', timeMinutesImpl);
    const timeHoursImpl = mkDuration('Time.hours', 60 * 60 * 1000);
    def('timeHours', timeHoursImpl);      def('Time.hours', timeHoursImpl);
    const timeDaysImpl = mkDuration('Time.days', 24 * 60 * 60 * 1000);
    def('timeDays', timeDaysImpl);        def('Time.days', timeDaysImpl);
    const timeWeeksImpl = mkDuration('Time.weeks', 7 * 24 * 60 * 60 * 1000);
    def('timeWeeks', timeWeeksImpl);      def('Time.weeks', timeWeeksImpl);
    // Whole seconds, toward zero: 1.5s is 1 and -1.5s is -1.
    def('timeToSeconds', native(1, ([d]) => VInt((d.ms - d.ms % 1000) / 1000)));
    def('Time.toSeconds', envLookup(env, 'timeToSeconds'));

    // Time.now: Task Time. Reads the wall clock; same shape as
    // Effect.succeed but the value is fresh on each run.
    def('timeNow', VEffect(() => timeOf('Time.now', Date.now()), 'timeNow'));
    def('Time.now', envLookup(env, 'timeNow'));

    // Time.every : Duration -> (Time -> msg) -> Sub msg, a recurring
    // subscription. Identity is the interval (seconds); the tagger is the
    // payload. The reconciler turns it into a setInterval that delivers the
    // current Time each tick. Fires first AFTER one interval (Elm's Time.every);
    // seed the immediate value with `Cmd.perform GotNow Time.now` in init.
    const timeEveryImpl = native(2, ([dur, tagger]) => {
      const ms = (dur && typeof dur.ms === 'number') ? dur.ms : 0;
      return { k: 'SUB', items: [{ src: 'timeEvery', key: 'timeEvery:' + ms, intervalMs: ms, tagger }] };
    });
    def('timeEvery', timeEveryImpl);
    def('Time.every', timeEveryImpl);

    // Keyboard.watch : ({ down : List Keyboard.Key } -> msg) -> Sub msg. Fixed
    // source key (one shared held-key mirror); the reconciler merges taggers.
    // See subSources.keyboardWatch for delivery.
    const keyboardWatchImpl = native(1, ([tagger]) => ({ k: 'SUB', items: [{ src: 'keyboardWatch', key: 'keyboardWatch', tagger }] }));
    def('keyboardWatch', keyboardWatchImpl); def('Keyboard.watch', keyboardWatchImpl);
    // Gamepad.watch : (pad -> msg) -> Sub msg. Fixed source key (one shared
    // poll); the reconciler merges taggers. See subSources.gamepadWatch.
    const gamepadWatchImpl = native(1, ([tagger]) => ({ k: 'SUB', items: [{ src: 'gamepadWatch', key: 'gamepadWatch', tagger }] }));
    def('gamepadWatch', gamepadWatchImpl); def('Gamepad.watch', gamepadWatchImpl);

    // ---- Device (docs/proposals/device.md): live capabilities, no UA guess ----
    // Pointer constructors: global (like Order's LT / Method's GET), nullary.
    def('Coarse', VCtor('Coarse')); def('Fine', VCtor('Fine'));
    // CanvasMode constructors: global, nullary; the mandatory first arg of
    // `canvas` (Pixelated = 1x + nearest-neighbour, Crisp = retina + smooth).
    def('Pixelated', VCtor('Pixelated')); def('Crisp', VCtor('Crisp'));
    // Device.watch : (Device -> msg) -> Sub msg, the deviceWatch sub source
    // (above) reads matchMedia + innerWidth/Height and fires the record.
    const deviceWatchImpl = native(1, ([tagger]) => ({ k: 'SUB', items: [{ src: 'deviceWatch', key: 'deviceWatch', tagger }] }));
    def('deviceWatch', deviceWatchImpl); def('Device.watch', deviceWatchImpl);
    // Device.touchOnly / canHover: pure readings off a Device record.
    const deviceField = (d, name) => (d && d.fields) ? d.fields[name] : undefined;
    const deviceTouchOnlyImpl = native(1, ([d]) => {
      const p = deviceField(d, 'pointer');
      const coarse = !!(p && p.tag === 'Coarse');
      const b = (name) => { const v = deviceField(d, name); return !!(v && v.b); };
      return VBool(coarse && !b('anyFine') && !b('supportsHover'));
    });
    const deviceCanHoverImpl = native(1, ([d]) => { const v = deviceField(d, 'supportsHover'); return VBool(!!(v && v.b)); });
    def('deviceTouchOnly', deviceTouchOnlyImpl); def('Device.touchOnly', deviceTouchOnlyImpl);
    def('deviceCanHover', deviceCanHoverImpl);   def('Device.canHover', deviceCanHoverImpl);

    // ---- Sound (docs/proposals/sound.md): chip-audio SFX + loops + beds ----
    // Wave constructors: values (the first arg to tone); the synth reads .tag.
    // Sound.Square / Triangle / Sawtooth / Noise constructors come from the
    // generated registry (see the GENERATED BUILTIN CTORS region).
    const mkSound = (voices) => ({ k: 'SND', voices });
    // Copy every field, not a named list. A whitelist here silently drops any
    // field a LATER combinator added: chaining two patches would keep only the
    // outermost one, and the loss is invisible (no error, just a sound that
    // ignores half of what it was asked for).
    const cloneVoices = (snd) => (snd && snd.voices) ? snd.voices.map(v => ({ ...v })) : [];
    // The identity of a HELD source: one oscillator kept alive by a Sub, as
    // opposed to a note scheduled and forgotten. Whatever is left OUT of the
    // identity becomes a live parameter: returning the sound again with only
    // that part changed glides the running node (soundGlideTo) instead of
    // stopping and restarting it, which would click.
    //
    // Volume is always out, so a held sound can swell. `withFreq` is the ONLY
    // difference between the two held Subs, and it is the whole difference:
    //
    //   Sound.voice  withFreq = true   pitch is identity  -> two pitches are
    //                                  two voices. Polyphony: a voice per key.
    //   Sound.glide  withFreq = false  pitch is a param   -> one source that
    //                                  slides to whatever pitch it is handed.
    //                                  Monophonic by construction, like glide
    //                                  on a synth. An engine note, a siren.
    //
    // Noise carries freq 0, so the flag changes nothing for it. The cuts DO
    // belong to the identity either way: the filter is built once when the
    // source starts and never glides, so a new shape has to be a new source or
    // it would keep the old filter.
    const heldKey = (snd, withFreq) => {
      try {
        return JSON.stringify(((snd && snd.voices) || []).map(v => [
          v.wave, withFreq ? v.freq : 0, v.ms, v.endFreq, v.holdMs, v.delayMs,
          v.duty, v.vibDepth, v.vibRate, v.tremDepth, v.tremRate, v.arp, v.lowCut, v.highCut,
          v.attack, v.release,
          // A different envelope is a different sound, so decay and its sustain
          // level belong in the identity exactly as attack and release do.
          v.decay, v.sustain,
          // detune is TIMBRE, not the live pitch. `withFreq` drops freq so a
          // Sound.glide can retune one live source; a detune is the fixed offset
          // that says WHICH layer of a unison this is, so changing it is a new
          // voice — the same call `duty` makes, two entries up.
          v.detune,
          // pan is in the identity for a harder reason than the others: there is
          // no handle on the pan gains anywhere outside soundPanOut, so
          // soundGlideTo cannot move a live source's position. Two Sound.voice
          // subs differing only in pan would hash the same, the second would
          // never start, and the two halves of a stereo pad would collapse into
          // whichever one began first.
          v.pan]));
      } catch (e) { return 'x'; }
    };
    // tone : Wave -> Int -> Int -> Sound  (wave, freq Hz, duration ms)
    const soundToneImpl = native(3, ([wave, freq, ms]) =>
      mkSound([{ wave: (wave && wave.tag) || 'Square', freq: freq.n, ms: ms.n, endFreq: 0, holdMs: 0, volume: 60, delayMs: 0, duty: 0, vibDepth: 0, vibRate: 0, tremDepth: 0, tremRate: 0, arp: null, lowCut: 0, highCut: 0, attack: 0, release: 0, decay: 0, sustain: 100, detune: 0, pan: 0 }]));
    def('soundTone', soundToneImpl); def('Sound.tone', soundToneImpl);
    const patchAll = (snd, f) => { const vs = cloneVoices(snd); vs.forEach(f); return mkSound(vs); };
    // ONE RULE: a combinator patches EVERY voice. `patchLast` is gone.
    //
    // It used to be split -- volume, duty, detune, sweep, holdPitch, lowCut,
    // highCut, vibrato and arp touched only the last voice, everything else
    // touched all of them -- and nothing in the type or the reference said
    // which. `highCut 3600 (chord [saw, pulseA, pulseB])` filtered pulseB and
    // left the other two open, silently. It was caught only because two takes of
    // the same guitar part measured 1.2 dB apart, which two identical things
    // cannot do.
    const soundVolumeImpl = native(2, ([n, snd]) => patchAll(snd, v => { v.volume = Math.max(0, Math.min(100, n.n)); }));
    def('soundVolume', soundVolumeImpl); def('Sound.volume', soundVolumeImpl);
    const soundSweepImpl = native(2, ([end, snd]) => patchAll(snd, v => { v.endFreq = end.n; }));
    def('soundSweep', soundSweepImpl); def('Sound.sweep', soundSweepImpl);
    // attack / release : the envelope, in ms. Carried by the VOICE so every
    // playback path renders the same shape: `once` and `loop` ramp it inside the
    // note's own span, `hold` fades in on start and out when the sub stops.
    //
    // A chord is one note played on several oscillators: if only one layer took
    // the attack, the others would still jump, so the note would both click and
    // speak twice. Per-layer envelopes are still expressible, the same way every
    // per-layer setting is: shape a tone BEFORE putting it in the chord.
    const soundAttackImpl = native(2, ([ms, snd]) => patchAll(snd, v => { v.attack = Math.max(0, ms.n); }));
    def('soundAttack', soundAttackImpl); def('Sound.attack', soundAttackImpl);
    const soundReleaseImpl = native(2, ([ms, snd]) => patchAll(snd, v => { v.release = Math.max(0, ms.n); }));
    def('soundRelease', soundReleaseImpl); def('Sound.release', soundReleaseImpl);
    // decay : the third stage. `decay ms level` falls from full volume to `level`
    // percent of it, then holds there until the release. patchAll for the same
    // reason attack and release are: a chord is one note on several oscillators.
    //
    // `decay 0 _` is off, and off is exactly the old shape — that is what let this
    // land without moving a single existing sound.
    const soundDecayImpl = native(3, ([ms, level, snd]) => patchAll(snd, v => {
      v.decay = Math.max(0, ms.n);
      v.sustain = Math.max(0, Math.min(100, level.n));
    }));
    def('soundDecay', soundDecayImpl); def('Sound.decay', soundDecayImpl);
    // pan : -100 hard left, 0 centre, +100 hard right. patchAll, with attack and
    // release and decay, and NOT with detune two blocks down even though the
    // signature is identical: a counter-melody is a Sound.sequence, so patching
    // the last voice would place one note of thirty and leave the rest centred,
    // which is the exact muddle pan exists to undo. Per-voice placement is still
    // expressible the same way per-voice envelopes are: pan a tone BEFORE putting
    // it in the chord.
    //
    // Clamped here AND re-clamped in soundPanOut. The law reads pan directly, so
    // an unclamped 400 would give gainL = -3: a polarity inversion three times
    // too loud, which cancels against the other voices instead of going quiet.
    const soundPanImpl = native(2, ([pos, snd]) => patchAll(snd, v => {
      v.pan = Math.max(-100, Math.min(100, pos.n));
    }));
    def('soundPan', soundPanImpl); def('Sound.pan', soundPanImpl);
    // lowCut / highCut : tone shaping. Separate combinators because a sound
    // usually wants only ONE end trimmed (wind cuts the lows, a sound heard
    // through a wall cuts the highs), and a single band-pass form would force a
    // sentinel for "leave this side alone". They stack when you want both.
    const soundLowCutImpl = native(2, ([hz, snd]) => patchAll(snd, v => { v.lowCut = Math.max(0, hz.n); }));
    def('soundLowCut', soundLowCutImpl); def('Sound.lowCut', soundLowCutImpl);
    const soundHighCutImpl = native(2, ([hz, snd]) => patchAll(snd, v => { v.highCut = Math.max(0, hz.n); }));
    def('soundHighCut', soundHighCutImpl); def('Sound.highCut', soundHighCutImpl);
    // hold : keep the pitch flat for the first `ms`, THEN let a `sweep` glide it
    // over the remaining time (one seamless wave: sits, then bends). No-op without a sweep.
    const soundHoldPitchImpl = native(2, ([ms, snd]) => patchAll(snd, v => { v.holdMs = Math.max(0, ms.n); }));
    def('soundHoldPitch', soundHoldPitchImpl); def('Sound.holdPitch', soundHoldPitchImpl);
    // Expressiveness pack. duty : Square pulse width % (12/25/50/75, Square only).
    // vibrato : sine wobble on the pitch (depth cents, rate Hz). arp : cycle the
    // pitch fast through these extra Hz + the base (chiptune "chord" on one voice).
    const soundDutyImpl = native(2, ([pct, snd]) => patchAll(snd, v => { v.duty = Math.max(1, Math.min(99, pct.n)); }));
    def('soundDuty', soundDutyImpl); def('Sound.duty', soundDutyImpl);
    // detune : signed cents.
    //
    // A unison is layers that DIFFER, so the way to write one is a chord of two
    // separately detuned tones -- not one detune over both, which now (correctly)
    // moves them together.
    //
    // Clamped to +/- 2400 (two octaves). Past that the number has stopped being a
    // detune and become a transpose, which `Sound.tone`'s own pitch already says
    // better — and the clamp keeps a typo from producing an inaudible note in one
    // runtime and a sub-hertz crawl in the other.
    const soundDetuneImpl = native(2, ([cents, snd]) => patchAll(snd, v => {
      v.detune = Math.max(-2400, Math.min(2400, cents.n));
    }));
    def('soundDetune', soundDetuneImpl); def('Sound.detune', soundDetuneImpl);
    const soundVibratoImpl = native(3, ([depth, rate, snd]) => patchAll(snd, v => { v.vibDepth = Math.max(0, depth.n); v.vibRate = Math.max(1, rate.n); }));
    // tremolo : the wobble in LOUDNESS, as vibrato is the wobble in pitch. Depth
    // is a percentage of the voice's own level rather than cents, because that
    // is the unit amplitude has; rate is Hz, as there. Clamped at 100, past
    // which there is nothing left to take away.
    const soundTremoloImpl = native(3, ([depth, rate, snd]) => patchAll(snd, v => { v.tremDepth = Math.max(0, Math.min(100, depth.n)); v.tremRate = Math.max(1, rate.n); }));
    def('soundTremolo', soundTremoloImpl); def('Sound.tremolo', soundTremoloImpl);
    def('soundVibrato', soundVibratoImpl); def('Sound.vibrato', soundVibratoImpl);
    // arp : SEMITONE OFFSETS from the voice's own pitch, cycled fast on one
    // oscillator -- the chiptune trick for a chord from a single channel. They
    // used to be absolute Hz while the reference said semitones; relative is
    // both the documented API and the correct one, because it distributes over a
    // chord and it survives Sound.transpose. With absolute Hz a transposed
    // passage moved its base note and left the arpeggio standing in the old key.
    const soundArpImpl = native(2, ([list, snd]) => patchAll(snd, v => {
      v.arp = ((list && list.xs) || []).map(x => x.n);
    }));
    def('soundArp', soundArpImpl); def('Sound.arp', soundArpImpl);
    // gain : a percentage OF the current level, so it lowers a part without
    // touching the balance inside it. `volume` is absolute and cannot say this.
    const soundGainImpl = native(2, ([pct, snd]) => patchAll(snd, v => {
      const cur = v.volume == null ? 60 : v.volume;
      v.volume = Math.max(0, Math.min(100, Math.round(cur * Math.max(0, pct.n) / 100)));
    }));
    def('soundGain', soundGainImpl); def('Sound.gain', soundGainImpl);
    // sweepBy : bend by a musical distance rather than to a fixed frequency, so
    // a chord keeps its intervals. A rest has no pitch and is left alone.
    const soundSweepByImpl = native(2, ([cents, snd]) => patchAll(snd, v => {
      if (!v.freq) return;
      v.endFreq = Math.max(1, Math.round(v.freq * Math.pow(2, cents.n / 1200)));
    }));
    def('soundSweepBy', soundSweepByImpl); def('Sound.sweepBy', soundSweepByImpl);
    // transpose : the same passage, n semitones away. Moves the pitch and any
    // sweep target with it; the arpeggio is offsets and therefore already moves.
    // A rest and an unpitched noise voice (freq 0) both stay where they are.
    const soundTransposeImpl = native(2, ([semis, snd]) => patchAll(snd, v => {
      const r = Math.pow(2, semis.n / 12);
      if (v.freq) v.freq = Math.max(1, Math.round(v.freq * r));
      if (v.endFreq) v.endFreq = Math.max(1, Math.round(v.endFreq * r));
    }));
    def('soundTranspose', soundTransposeImpl); def('Sound.transpose', soundTransposeImpl);
    // stretch : the same passage at a percentage of its speed. 50 is half speed,
    // 200 is double. EVERY time in the voice scales -- its length, its place in
    // the sequence, and its envelope -- because a note played at half speed is a
    // longer note, not a short note that arrives late.
    const soundStretchImpl = native(2, ([pct, snd]) => patchAll(snd, v => {
      const k = Math.max(1, pct.n) / 100;
      const at = (x) => Math.max(0, Math.round((x || 0) / k));
      // Scale the END of the note, not its LENGTH. Rounding each duration on its
      // own breaks the one invariant a chord has -- every voice must span the
      // same total ms -- because two voices that ended together can round apart
      // by a millisecond, and under Sound.loop that millisecond accumulates on
      // every pass until the parts are playing different music. Rounding the
      // absolute end time instead gives every voice that ended at T the same
      // new end, so a chord that was square stays square.
      const start = v.delayMs || 0;
      const end = start + (v.ms || 0);
      v.delayMs = at(start);
      v.ms = Math.max(1, at(end) - v.delayMs);
      v.holdMs = at(v.holdMs);
      v.attack = at(v.attack); v.release = at(v.release); v.decay = at(v.decay);
    }));
    def('soundStretch', soundStretchImpl); def('Sound.stretch', soundStretchImpl);
    // chord : layer voices together. sequence : back-to-back, each part delayed
    // by the running total of the previous parts' lengths.
    const soundChordImpl = native(1, ([list]) => {
      const voices = [];
      for (const s of (list && list.xs) || []) for (const v of cloneVoices(s)) voices.push(v);
      return mkSound(voices);
    });
    def('soundChord', soundChordImpl); def('Sound.chord', soundChordImpl);
    const soundSequenceImpl = native(1, ([list]) => {
      const voices = []; let off = 0;
      for (const s of (list && list.xs) || []) {
        let span = 0;
        for (const v of cloneVoices(s)) {
          const base = v.delayMs || 0;
          v.delayMs = base + off;
          span = Math.max(span, base + (v.ms || 0));
          voices.push(v);
        }
        off += span;
      }
      return mkSound(voices);
    });
    def('soundSequence', soundSequenceImpl); def('Sound.sequence', soundSequenceImpl);
    // rest : Int -> Sound  (silence of `ms`; occupies time in a sequence, no voice).
    // A rest carries the SAME field set a tone does, defaults included. It is
    // silent either way, so this is not audible -- but iOS reads a missing
    // `sustain` as 100 (the "unasked" value) while a field absent here reads as
    // 0, and the two runtimes then disagreed about what a rest IS. The sound
    // conformance corpus caught exactly that. A field that exists on one side
    // only is the bug this corpus is for.
    const soundRestImpl = native(1, ([ms]) => mkSound([{ wave: 'Rest', freq: 0, ms: ms.n, endFreq: 0, holdMs: 0, volume: 0, delayMs: 0, tremDepth: 0, tremRate: 0, decay: 0, sustain: 100, detune: 0, pan: 0 }]));
    def('soundRest', soundRestImpl); def('Sound.rest', soundRestImpl);
    // play : Sound -> Cmd msg  (fire once).
    // A page that is not on screen does not play sound behind the one that is
    // (refusedFromCovered).
    const soundPlayImpl = native(1, ([snd]) => VEffect(() => {
      if (!refusedFromCovered('Sound.play')) soundPlayNow(snd);
      return VUnit();
    }, 'soundPlay'));
    def('soundPlay', soundPlayImpl); def('Sound.play', soundPlayImpl);
    // loop : Sound -> Sub msg  (replay seamlessly while subscribed). Keyed by FULL
    // content INCLUDING volume, so any change swaps the song (a restart): fine for
    // BGM. ambient : Sound -> Sub msg  (steady bed; keyed WITHOUT volume so the same
    // bed at a new level retunes live instead of restarting: see subSources).
    const soundFullKey = (snd) => { try { return JSON.stringify((snd && snd.voices) || []); } catch (e) { return 'x'; } };
    const soundLoopImpl = native(1, ([snd]) => ({ k: 'SUB', items: [{ src: 'loop', key: 'loop:' + soundFullKey(snd), sound: snd, tagger: null }] }));
    def('soundLoop', soundLoopImpl); def('Sound.loop', soundLoopImpl);
    // once : Sound -> Sub msg (play through a single time; unsubscribe cancels).
    const soundOnceImpl = native(1, ([snd]) => ({ k: 'SUB', items: [{ src: 'once', key: 'once:' + soundFullKey(snd), sound: snd, tagger: null }] }));
    def('soundOnce', soundOnceImpl); def('Sound.once', soundOnceImpl);
    const soundVoiceImpl = native(1, ([snd]) => ({ k: 'SUB', items: [{ src: 'voice', key: 'voice:' + heldKey(snd, true), sound: snd, tagger: null }] }));
    def('soundVoice', soundVoiceImpl); def('Sound.voice', soundVoiceImpl);
    const soundGlideImpl = native(1, ([snd]) => ({ k: 'SUB', items: [{ src: 'glide', key: 'glide:' + heldKey(snd, false), sound: snd, tagger: null }] }));
    def('soundGlide', soundGlideImpl); def('Sound.glide', soundGlideImpl);
    // App-owned audio. setMuted ducks the master gain (silences beds/loops already
    // sounding, not just new plays); master sets a 0..100 volume.
    const soundSetMutedImpl = native(1, ([b]) => VEffect(() => { soundMuted = !!(b && b.b); applyMaster(); return VUnit(); }, 'soundSetMuted'));
    def('soundSetMuted', soundSetMutedImpl); def('Sound.setMuted', soundSetMutedImpl);
    // Sound.master 100 is the DEFAULT level, not louder than it. It used to map
    // to 0.5 against a default of 0.35, so a settings screen with a 0..100 slider
    // sitting at 100 -- which is where every settings screen sits -- shipped the
    // game 3.1 dB louder than it was mixed and ate the headroom the ceiling is
    // budgeted against. The safe value should not be an odd number.
    const soundMasterImpl = native(1, ([n]) => VEffect(() => { soundMasterLevel = Math.max(0, Math.min(100, (n && n.n) || 0)) / 100 * 0.35; applyMaster(); return VUnit(); }, 'soundMaster'));
    def('soundMaster', soundMasterImpl); def('Sound.master', soundMasterImpl);
    // Note helpers: Sound.<name> octave -> Hz (equal temperament, A4 = 440). The
    // arg to mkPitch is the semitone above C. Kills the magic-Hz table for melodies.
    // (Defs are written as string literals, not a loop, so the drift test sees them.)
    const soundPitchHz = (semitone, oct) => { const midi = 12 * (oct + 1) + semitone; return Math.round(440 * Math.pow(2, (midi - 69) / 12)); };
    const mkPitch = (semi) => native(1, ([oct]) => VInt(soundPitchHz(semi, (oct && oct.n) || 0)));
    const pC = mkPitch(0);   def('soundPitch_c', pC);    def('Sound.c', pC);
    const pCs = mkPitch(1);  def('soundPitch_cs', pCs);  def('Sound.cs', pCs);
    const pD = mkPitch(2);   def('soundPitch_d', pD);    def('Sound.d', pD);
    const pDs = mkPitch(3);  def('soundPitch_ds', pDs);  def('Sound.ds', pDs);
    const pE = mkPitch(4);   def('soundPitch_e', pE);    def('Sound.e', pE);
    const pF = mkPitch(5);   def('soundPitch_f', pF);    def('Sound.f', pF);
    const pFs = mkPitch(6);  def('soundPitch_fs', pFs);  def('Sound.fs', pFs);
    const pG = mkPitch(7);   def('soundPitch_g', pG);    def('Sound.g', pG);
    const pGs = mkPitch(8);  def('soundPitch_gs', pGs);  def('Sound.gs', pGs);
    const pA = mkPitch(9);   def('soundPitch_a', pA);    def('Sound.a', pA);
    const pAs = mkPitch(10); def('soundPitch_as_', pAs); def('Sound.as_', pAs);
    const pB = mkPitch(11);  def('soundPitch_b', pB);    def('Sound.b', pB);

    // Time arithmetic: a Duration and a Time are both milliseconds, so a
    // shift is exact and Time.diff is to the millisecond.
    def('timeAdd', native(2, ([t, d]) => timeOf('Time.add', t.millis + d.ms)));
    def('Time.add', envLookup(env, 'timeAdd'));
    def('timeSub', native(2, ([t, d]) => timeOf('Time.sub', t.millis - d.ms)));
    def('Time.sub', envLookup(env, 'timeSub'));
    def('timeDiff', native(2, ([a, b]) => {
      const ms = (b.millis || 0) - (a.millis || 0);
      if (!Number.isSafeInteger(ms)) throw durationOutOfRange('Time.diff');
      return VDuration(ms);
    }));
    def('Time.diff', envLookup(env, 'timeDiff'));

    def('timeBefore', native(2, ([a, b]) => VBool((a.millis || 0) < (b.millis || 0))));
    def('Time.before', envLookup(env, 'timeBefore'));
    def('timeAfter',  native(2, ([a, b]) => VBool((a.millis || 0) > (b.millis || 0))));
    def('Time.after', envLookup(env, 'timeAfter'));

    // Time.toIso → ISO 8601 UTC. Wire format used by the server.
    // Every Time is in 0000..9999, so the year has four digits.
    def('timeToIso', native(1, ([t]) => VString(new Date(t.millis).toISOString())));
    def('Time.toIso', envLookup(env, 'timeToIso'));
    // Time.fromIso → Maybe Time: Nothing when the text is not in the one
    // form parseTimeISO reads, or names a moment outside the range.
    def('timeFromIso', native(1, ([s]) => {
      const ms = parseTimeISO(s.s);
      if (ms === null || !inTimeRange(ms)) return VCtor('Nothing', []);
      return VCtor('Just', [VTime(ms)]);
    }));
    def('Time.fromIso', envLookup(env, 'timeFromIso'));

    def('timeToMillis', native(1, ([t]) => VInt(t.millis)));
    def('Time.toMillis', envLookup(env, 'timeToMillis'));

    // Calendar arithmetic, by the helpers at the top of this file (as Go's
    // timerange.go). Days are exact; months and years land on the last day
    // of a shorter month: addMonths(2026-01-31, 1) is 2026-02-28.
    def('timeFromYMD', native(3, ([y, m, d]) => timeFromYMD(y.n, m.n, d.n)));
    def('Time.fromYMD', envLookup(env, 'timeFromYMD'));
    const calendarShift = (shift) => native(2, ([t, n]) => shift(t, n.n));
    def('timeAddDays',   calendarShift((t, n) => timeAddDays(t, n)));
    def('Time.addDays',  envLookup(env, 'timeAddDays'));
    def('timeAddMonths', calendarShift((t, n) => timeAddMonths(t, n, 'Time.addMonths ' + n)));
    def('Time.addMonths', envLookup(env, 'timeAddMonths'));
    def('timeAddYears',  calendarShift((t, n) => timeAddMonths(t, n * 12, 'Time.addYears ' + n)));
    def('Time.addYears', envLookup(env, 'timeAddYears'));

    // Component getters (UTC). Month is 1-indexed externally:
    // we add 1 to JS's getUTCMonth (0-indexed) so user code reads
    // the same values it'd see on the Go and iOS sides.
    const component = (extract) => native(1, ([t]) => VInt(extract(new Date(t.millis))));
    def('timeYear',    component(d => d.getUTCFullYear()));
    def('Time.year',   envLookup(env, 'timeYear'));
    def('timeMonth',   component(d => d.getUTCMonth() + 1));
    def('Time.month',  envLookup(env, 'timeMonth'));
    def('timeDay',     component(d => d.getUTCDate()));
    def('Time.day',    envLookup(env, 'timeDay'));
    def('timeHour',    component(d => d.getUTCHours()));
    def('Time.hour',   envLookup(env, 'timeHour'));
    def('timeMinute', component(d => d.getUTCMinutes()));
    def('Time.minute', envLookup(env, 'timeMinute'));
    def('timeSecond', component(d => d.getUTCSeconds()));
    def('Time.second', envLookup(env, 'timeSecond'));

    // Date: a day of the calendar, by the helpers at the top of this file
    // (as Go's date.go).
    def('dateFromYMD', native(3, ([y, m, d]) => dateFromYMD(y.n, m.n, d.n)));
    def('Date.fromYMD', envLookup(env, 'dateFromYMD'));
    const dateField = (extract) => native(1, ([dt]) => VInt(extract(civilFromDays(dt.days), dt)));
    def('dateYear', dateField(c => c.y));
    def('Date.year', envLookup(env, 'dateYear'));
    def('dateMonth', dateField(c => c.m));
    def('Date.month', envLookup(env, 'dateMonth'));
    def('dateDay', dateField(c => c.d));
    def('Date.day', envLookup(env, 'dateDay'));
    // 1 for Monday through 7 for Sunday; 1970-01-01 was a Thursday.
    def('dateWeekday', dateField((c, dt) => posMod(dt.days + 3, 7) + 1));
    def('Date.weekday', envLookup(env, 'dateWeekday'));
    const dateShift = (shift) => native(2, ([dt, n]) => shift(dt, n.n));
    def('dateAddDays', dateShift((dt, n) => dateOf('Date.addDays ' + n, dt.days + n)));
    def('Date.addDays', envLookup(env, 'dateAddDays'));
    def('dateAddMonths', dateShift((dt, n) => dateAddMonths(dt, n, 'Date.addMonths ' + n)));
    def('Date.addMonths', envLookup(env, 'dateAddMonths'));
    def('dateAddYears', dateShift((dt, n) => dateAddMonths(dt, n * 12, 'Date.addYears ' + n)));
    def('Date.addYears', envLookup(env, 'dateAddYears'));
    // Date.diffDays a b: the days FROM a TO b.
    def('dateDiffDays', native(2, ([a, b]) => VInt(b.days - a.days)));
    def('Date.diffDays', envLookup(env, 'dateDiffDays'));
    def('dateBefore', native(2, ([a, b]) => VBool(a.days < b.days)));
    def('Date.before', envLookup(env, 'dateBefore'));
    def('dateAfter', native(2, ([a, b]) => VBool(a.days > b.days)));
    def('Date.after', envLookup(env, 'dateAfter'));
    def('dateToIso', native(1, ([dt]) => VString(formatDateISO(dt))));
    def('Date.toIso', envLookup(env, 'dateToIso'));
    def('dateFromIso', native(1, ([str]) => {
      const days = parseDateISO(str.s);
      return days === null ? VCtor('Nothing', []) : VCtor('Just', [dateOf(str.s, days)]);
    }));
    def('Date.fromIso', envLookup(env, 'dateFromIso'));
    // The day a Time falls on in UTC, and midnight UTC of a Date.
    def('dateFromTime', native(1, ([t]) => dateOf('Date.fromTime', Math.floor(t.millis / MS_PER_DAY))));
    def('Date.fromTime', envLookup(env, 'dateFromTime'));
    def('dateToTime', native(1, ([dt]) => timeOf('Date.toTime', dt.days * MS_PER_DAY)));
    def('Date.toTime', envLookup(env, 'dateToTime'));
    // Date.today: Task Date, the device's clock read in the device's time
    // zone (localDay).
    def('dateToday', VEffect(() => dateOf('Date.today', localDay(Date.now(), localOffsets())), 'dateToday'));
    def('Date.today', envLookup(env, 'dateToday'));

    // JSON.decode : String -> Result String α, uses the IIFE-level
    // jsToMar so that fetchAuthMe (in mountPages) can reach the
    // same decoder.
    // Only a document that does not parse is "not valid JSON"; a tagged
    // value that cannot be one is refused in its own words, as
    // decodeWithShape and the other two runtimes do.
    def('jsonDecode', native(1, ([raw]) => {
      let parsed;
      try {
        parsed = parseJSONExact(raw.s);
      } catch (e) {
        return VCtor('Err', [VString('the document is not valid JSON')]);
      }
      try {
        return VCtor('Ok', [jsToMar(parsed)]);
      } catch (e) {
        return VCtor('Err', [VString(String((e && e.message) || e))]);
      }
    }));
    def('JSON.decode', envLookup(env, 'jsonDecode'));

    // JSON.encode : α -> String, uses the IIFE-level marToJs so
    // every consumer (jsonEncode, Service.call body, authPost
    // body) shares the same encoder. Every ctor, Maybe and Result
    // included, round-trips via the {__ctor, __args} marker.
    def('jsonEncode', native(1, ([v]) => VString(JSON.stringify(marToJs(v)))));
    def('JSON.encode', envLookup(env, 'jsonEncode'));

    // Http.get / Http.post: async fetch wrapped in an Effect.
    //
    //   Http.get  : String -> (Result String String -> msg) -> Effect Never msg
    //   Http.post : String -> String -> (Result String String -> msg) -> Effect Never msg
    //
    // The third (toMsg) argument lets the call result be turned into a Msg
    // that gets dispatched into the running app. The Effect itself does not
    // produce a value synchronously: the response arrives asynchronously
    // and is delivered as a Msg.
    def('httpGet', native(2, ([url, toMsg]) => {
      return VEffect(() => {
        const issuer = effectIssuer;
        fetch(url.s)
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(decodeServerError(r.body) || ('HTTP ' + (r.status || 0)))]);
            const msg = apply(toMsg, result);
            deliverFrom(issuer, msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            deliverFrom(issuer, msg);
          });
        return VUnit();
      }, 'httpGet');
    }));
    def('Http.get', envLookup(env, 'httpGet'));

    // Entity.* and Repo.* are NOT defined here, on purpose.
    //
    // They used to be, as inert stubs, because a page that imported a module
    // for one type alias dragged that module's schema into the browser, where
    // `Entity.define` then had to evaluate. The bundler now ships only the
    // declarations a page actually reaches (ADR 0019), so server-only code no
    // longer arrives at all, and the stubs were the thing keeping that
    // failure quiet. Forgetting one is what made `Entity.enum` typecheck and
    // then die in the browser with "unbound name".
    //
    // Leaving them out means a regression in the pruner surfaces immediately
    // as an unbound name instead of silently doing nothing, and PickFrontMods
    // refuses the build before that anyway.

    // Service (RPC over HTTP). Server-side wraps a handler; browser-side
    // the handler is never invoked: the contract just carries the verb +
    // path so Service.call can build the request.
    //
    // Service.declare VERB "path": a typed RPC contract carrying the HTTP
    // verb and URL pattern. The browser never runs the handler; it only
    // needs the verb + path so Service.call can build the request.
    def('serviceDeclare', native(2, ([method, path]) => {
      const svc = VCtor('__Service', []);
      svc.verb = (method && method.tag) || 'POST';
      svc.path = (path && path.s) || '/';
      return svc;
    }));
    def('Service.declare', envLookup(env, 'serviceDeclare'));

    // Service.implement / Auth.protect: pair a contract with a
    // handler. Server-side these produce ExposedService values the
    // dispatcher mounts; browser-side the handler is never invoked,
    // so we just return the contract back so the value evaluates and
    // any downstream references stay well-typed at runtime.
    def('serviceImplement', native(2, ([contract, _handler]) => contract));
    def('Service.implement', envLookup(env, 'serviceImplement'));

    def('authProtect', native(2, ([contract, _handler]) => contract));
    def('Auth.protect', envLookup(env, 'authProtect'));

    // PROPOSAL stubs (docs/authorization-proposal.md). The browser
    // never enforces auth: the server's dispatcher does. So these
    // are pass-throughs returning the ExposedService unchanged.
    def('authRequireRole',  native(2, ([_role, exposed]) => exposed));
    def('Auth.requireRole', envLookup(env, 'authRequireRole'));
    def('authAuthorize',    native(3, ([_load, _policy, exposed]) => exposed));
    def('Auth.authorize',   envLookup(env, 'authAuthorize'));
    def('authRequireOwner', native(3, ([_load, _sel, exposed]) => exposed));
    def('Auth.requireOwner', envLookup(env, 'authRequireOwner'));

    // Auth.config: server-side captures the user entity + signup hook
    // + email config into a global for the framework HTTP handlers.
    // Browser-side we additionally pull the `signInPage` field's
    // path off: Page.protected reads it as the redirect target when
    // the user has no session. Stored on globalThis so the closure
    // inside mountPages (set up later) can read it without explicit
    // wiring.
    def('authConfig', native(1, ([cfg]) => {
      if (cfg && cfg.fields && cfg.fields.signInPage) {
        const page = cfg.fields.signInPage;
        // The page is a __Page or __ProtectedPage ctor; first arg
        // is the path string.
        if (page.k === 'C' && page.args && page.args.length > 0) {
          const pathV = page.args[0];
          if (pathV && pathV.k === 'S') {
            globalThis.__marAuthSignInPath = pathV.s;
          }
        }
      }
      return VCtor('__Auth', [cfg]);
    }));
    def('Auth.config', envLookup(env, 'authConfig'));

    // Service.call : Service req resp -> req -> (Result Service.Error resp -> msg) -> Cmd msg
    //   - Builds the request from the contract's verb + path: typed path
    //     params go in the URL, the rest in the query (GET / DELETE) or
    //     the JSON body (POST / PUT / PATCH). Parses the JSON response into
    //     a mar value, dispatches msg(Ok | Err).
    // A Service.call occurrence whose declared RESPONSE type the checker
    // knew. Same call, one more question asked of what comes back.
    serviceCallWithShape = (shape) => native(3, ([svc, req, toMsg]) =>
      serviceCallImpl(svc, req, toMsg, shape));

    def('serviceCall', native(3, ([svc, req, toMsg]) => serviceCallImpl(svc, req, toMsg, null)));

    function serviceCallImpl(svc, req, toMsg, respShape) {
      return VEffect(() => {
        const issuer = effectIssuer;
        const verb = svc.verb || 'POST';
        const built = buildServiceRequest(verb, svc.path || '/', req);
        const init = { method: verb, credentials: 'same-origin', headers: {} };
        if (built.body != null) {
          init.body = built.body;
          init.headers['Content-Type'] = 'application/json';
        }
        fetch(built.url, init)
          // Read the server's identity here, while the Response still
          // exists: the next step reduces it to {ok, body, status} and
          // the headers are gone. Service calls are the traffic a
          // long-lived tab actually generates, which makes this the
          // one hook that matters.
          .then(r => { noteServerIdentity(r); return r; })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            // Auth-expiry interceptor: a 401 on a Service.call means
            // the session is gone (or never existed). Send the user to
            // the configured signInPath, capturing where they were so
            // Auth.completeSignIn can return them after a successful login.
            // The Err is NOT dispatched: user code never sees this
            // case. This keeps "session expired" out of every update
            // function across the app.
            if (r.status === 401 && handleAuthExpired()) return;
            if (!r.ok) {
              const msg = apply(toMsg, VCtor('Err', [serviceErrorFromResponse(r.status, r.body)]));
              deliverFrom(issuer, msg);
              return;
            }
            let parsed;
            try {
              parsed = jsToMar(jsonBodyOrThrow(r.body));
            } catch (e) {
              // Response arrived but its body was not decodable: a server
              // contract bug, surfaced as a ServerError so the app can show it.
              const msg = apply(toMsg, VCtor('Err', [VCtor('ServerError', [VString('decode failed: ' + (e && e.message || e))])]));
              deliverFrom(issuer, msg);
              return;
            }
            // The response was not produced by the type system either. It
            // comes from this app's own server, so the realistic mismatch is
            // version skew: an open tab, or a phone still on an embedded
            // snapshot, against a server whose response type has moved. It
            // goes where a body that would not decode already goes.
            if (respShape) {
              const shapeErr = checkWireAt(parsed, respShape, '');
              if (shapeErr) {
                const bad = apply(toMsg, VCtor('Err', [VCtor('ServerError',
                  [VString('the response does not match what this app expects: ' + shapeErr)])]));
                deliverFrom(issuer, bad);
                return;
              }
              parsed = coerceWire(parsed, respShape);
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            deliverFrom(issuer, msg);
          })
          .catch(err => {
            // fetch() rejects only when the request never completed (offline,
            // DNS, connection dropped): that is the Offline case.
            const msg = apply(toMsg, VCtor('Err', [serviceErrorOffline()]));
            deliverFrom(issuer, msg);
          });
        return VUnit();
      }, 'serviceCall');
    }
    def('Service.call', envLookup(env, 'serviceCall'));

    // friendlyFetchError translates a fetch() rejection into a
    // user-facing string. fetch() only rejects on actual network
    // failures (DNS down, server unreachable, request aborted,
    // CORS blocked, browser offline); HTTP error statuses come
    // through .then(r => ...) with r.ok=false, NOT here.
    //
    // The raw err.message varies by browser:
    //   - Safari:  "Load failed"
    //   - Chrome:  "Failed to fetch"
    //   - Firefox: "NetworkError when attempting to fetch resource"
    //
    // None of those are useful to an end user. We collapse them
    // into one calm sentence (two, when navigator.onLine confirms
    // we're offline) and stash the raw text in the console for the
    // developer debugging the issue.
    //
    // Used by every Service.call / Auth.* fetch
    // .catch handler so apps consistently see clean error strings
    // in their `Result String _` channels.
    function friendlyFetchError(err) {
      if (typeof console !== 'undefined' && console.warn) {
        console.warn('[mar] network failure:', err);
      }
      const offline = (typeof navigator !== 'undefined' && navigator.onLine === false);
      return offline
        ? "You appear to be offline. Check your connection and try again."
        : "Couldn't reach the server. Try again in a moment.";
    }

    // handleAuthExpired centralizes the "401 from Service.call" reaction:
    //
    //   1. If signInPath isn't configured (Auth.config absent), can't
    //      do anything useful: return false so the caller surfaces the
    //      Err to user code instead. Without this guard we'd swallow
    //      the error silently in apps that don't even use Auth.
    //   2. If we already navigated to sign-in for an earlier 401 in
    //      this batch (parallel Service.calls all expiring together),
    //      drop subsequent 401s on the floor: one redirect is enough.
    //      The flag is reset by Auth.completeSignIn after a successful
    //      verifyCode, so the next legitimate session expiry will
    //      redirect again.
    //   3. Otherwise: capture the current path as `?next=`, navigate
    //      to the sign-in URL via replaceFresh (which also invalidates
    //      the cached user: without that the next protected render
    //      would still see Just user and loop back to 401).
    //
    // Returns true if the redirect was triggered (or coalesced); false
    // means the caller should fall through to its default error path.
    function handleAuthExpired() {
      const signInPath = globalThis.__marAuthSignInPath;
      let here = '';
      try { here = window.location.pathname + window.location.search; } catch (_) { here = ''; }
      const d = decideAuthExpiry(signInPath, !!globalThis.__marRedirectingToSignIn, here);
      if (!d.handled) return false;
      if (!d.navigate) return true;
      const nav = globalThis.__marNav;
      if (!nav) return false;

      globalThis.__marRedirectingToSignIn = true;

      // isSafeReturnPath is this platform's half: the captured path rides in a
      // URL here, so it has to survive being typed by somebody else.
      let target = signInPath;
      if (d.capture && isSafeReturnPath(d.capture)) {
        target = signInPath + '?next=' + encodeURIComponent(d.capture);
      }

      // replaceFresh resets marDepth to 0: /sign-in is an entry
      // point, regardless of how deep into the app the user was
      // when their session expired. Without the reset, a user
      // bounced from /tasks/edit/42 (depth 1) would see a back
      // button on /sign-in pointing at a page they can no longer
      // access.
      nav.replaceFresh(target);
      return true;
    }

    // ----- Auth client Effects -----
    //
    // The four framework HTTP endpoints under /_auth/*. Each returns
    // an Effect that fires the request, parses the response, and
    // dispatches Ok/Err into the user's Msg via toMsg. Mirrors the
    // Service.call shape so user code looks identical.
    function authPost(path, body, toMsg, decodeOk) {
      return VEffect(() => {
        const issuer = effectIssuer;
        fetch(path, {
          method: 'POST',
          credentials: 'same-origin',
          body: body == null ? null : JSON.stringify(body),
          headers: body == null ? {} : { 'Content-Type': 'application/json' },
        })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            if (!r.ok) {
              const errStr = decodeAuthError(r.body) || ('HTTP ' + r.status);
              const msg = apply(toMsg, VCtor('Err', [VString(errStr)]));
              deliverFrom(issuer, msg);
              return;
            }
            let parsed;
            try {
              parsed = decodeOk(r.body);
            } catch (e) {
              const msg = apply(toMsg, VCtor('Err', [VString('decode failed: ' + (e && e.message || e))]));
              deliverFrom(issuer, msg);
              return;
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            deliverFrom(issuer, msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            deliverFrom(issuer, msg);
          });
        return VUnit();
      }, 'authPost');
    }

    // decodeServerError tries to extract a stable error code from an
    // HTTP error response body. The Mar runtime + framework endpoints
    // (auth, rate-limit middleware, admin) consistently shape error
    // bodies as `{"error": "snake_case_code", ...}`. When that shape
    // is present, return just the code so user code can `case` on it
    // cleanly. Otherwise fall back to the raw body or empty.
    //
    // Without this, a 429 from the gateway limiter would surface in
    // a Result.Err as the literal string
    //   `{"error":"rate_limited","retryAfterSeconds":3}`
    // which apps would have to substring-match against: defeating
    // the whole point of having stable codes server-side.
    function decodeServerError(body) {
      if (!body) return '';
      try {
        const j = JSON.parse(body);
        if (j && typeof j.error === 'string') {
          return wellFormed(j.error);
        }
      } catch (_) { /* fall through to raw body */ }
      return body;
    }

    // serviceErrorFromResponse / serviceErrorOffline build the Service.Error
    // union a Service.call delivers in its Err. Mirrors the Go runtime
    // (serviceErrorString) and Swift. Offline = request never reached the
    // server; Unauthorized = 401; RateLimited = 429 (gateway limiter);
    // ServerError = anything else, carrying the server's message (the
    // operator's snake_case code, intact).
    function serviceErrorOffline() { return VCtor('Offline'); }
    function serviceErrorFromResponse(status, body) {
      if (status === 401) return VCtor('Unauthorized');
      if (status === 429) return VCtor('RateLimited');
      return VCtor('ServerError', [VString(decodeServerError(body) || ('HTTP ' + (status || 0)))]);
    }
    // serviceErrorToStringJS folds a Service.Error to its default display
    // string. Keep these messages identical to the Go and Swift runtimes.
    function serviceErrorToStringJS(e) {
      if (!e || e.tag === undefined) return '';
      switch (e.tag) {
        case 'Offline': return "Can't reach the server. Check your connection and try again.";
        case 'Unauthorized': return 'Your session has expired. Please sign in again.';
        case 'RateLimited': return 'Too many requests. Wait a moment and try again.';
        case 'ServerError': return (e.args && e.args[0] && e.args[0].s) || '';
        default: return e.tag;
      }
    }

    // authOutcomePost drives Auth.requestCode / Auth.verifyCode. Domain
    // codes the endpoint is known to emit become typed outcome
    // constructors in the Ok (the screen cases on them); everything else
    // is transport and becomes a Service.Error in the Err, exactly like
    // Service.call. `mapCode` returns the outcome VCtor for a known
    // domain code or null. Mirrors the Swift MarHTTP boundary.
    function authOutcomePost(path, body, toMsg, okOutcome, mapCode) {
      return VEffect(() => {
        const issuer = effectIssuer;
        fetch(path, {
          method: 'POST',
          credentials: 'same-origin',
          body: body == null ? null : JSON.stringify(body),
          headers: body == null ? {} : { 'Content-Type': 'application/json' },
        })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            let result;
            if (r.ok) {
              try {
                result = VCtor('Ok', [okOutcome(r.body)]);
              } catch (e) {
                // A shape mismatch already carries the whole sentence; only a
                // parse failure needs the `decode failed:` framing. Before the
                // auth port had a shape at all, a body of the wrong shape got
                // no sentence: reading a missing field threw inside the
                // promise and the outer .catch answered Offline, which told
                // the user their connection was down about a server that had
                // answered.
                const text = (e && e.marWire) ? e.message
                  : 'decode failed: ' + (e && e.message || e);
                result = VCtor('Err', [VCtor('ServerError', [VString(text)])]);
              }
            } else {
              const domain = mapCode(decodeServerError(r.body));
              result = domain
                ? VCtor('Ok', [domain])
                : VCtor('Err', [serviceErrorFromResponse(r.status, r.body)]);
            }
            deliverFrom(issuer, apply(toMsg, result));
          })
          .catch(() => {
            deliverFrom(issuer, apply(toMsg, VCtor('Err', [serviceErrorOffline()])));
          });
        return VUnit();
      }, 'authPost');
    }

    def('authRequestCode', native(2, ([req, toMsg]) => {
      // CodeSent never reveals whether the email has an account: the
      // server answers 200 for unknown emails on purpose.
      return authOutcomePost('/_auth/request-code', marToJs(req), toMsg,
        () => VCtor('CodeSent'),
        code => code === 'rate_limited' ? VCtor('RateLimited')
              : code === 'invalid_email' ? VCtor('InvalidEmail')
              : null);
    }));
    def('Auth.requestCode', envLookup(env, 'authRequestCode'));

    // The user the sign-in endpoint answers with is checked against the type
    // the call site asked for, exactly like a Service.call response. Without
    // the shape this was the one wire port that asserted nothing.
    authVerifyWithShape = (shape) => native(2, ([req, toMsg]) => {
      return authOutcomePost('/_auth/verify-code', marToJs(req), toMsg,
        text => VCtor('SignedIn', [checkedUser(text, shape)]),
        code => code === 'invalid_code' ? VCtor('WrongCode')
              : code === 'too_many_attempts' ? VCtor('TooManyAttempts')
              : null);
    });

    def('authVerifyCode', native(2, ([req, toMsg]) => {
      return authOutcomePost('/_auth/verify-code', marToJs(req), toMsg,
        text => VCtor('SignedIn', [jsToMar(JSON.parse(text))]),
        code => code === 'invalid_code' ? VCtor('WrongCode')
              : code === 'too_many_attempts' ? VCtor('TooManyAttempts')
              : null);
    }));
    def('Auth.verifyCode', envLookup(env, 'authVerifyCode'));

    // Auth outcome constructors: qualified-only, like Service.Error.
    def('Auth.CodeSent', VCtor('CodeSent'));
    def('Auth.InvalidEmail', VCtor('InvalidEmail'));
    def('Auth.RateLimited', VCtor('RateLimited'));
    def('Auth.WrongCode', VCtor('WrongCode'));
    def('Auth.TooManyAttempts', VCtor('TooManyAttempts'));
    def('Auth.SignedIn', native(1, args => VCtor('SignedIn', [args[0]])));

    def('authLogout', native(1, ([toMsg]) => {
      // Optimistic logout: dispatch the result message IMMEDIATELY
      // and fire the server request in the background as
      // fire-and-forget. This is what production webapps
      // (Gmail / Slack / Twitter) do: logout is fundamentally
      // a UI gesture, not a transaction. Waiting for the server
      // to clear its session before the user sees ANY response
      // means a slow network leaves the operator stuck on a
      // protected page with no feedback ("did my tap register?").
      //
      // Trade-off: if the server never receives the POST (network
      // dropped, server down), the session cookie remains valid
      // server-side until it expires naturally. The client UI has
      // already moved to the sign-in page; if the user reloads,
      // Page.protected's /_auth/whoami check would see the still-
      // valid cookie and route them back to the protected page.
      // Acceptable in practice: rare network failure + rare
      // reload-immediately-after-logout combo. The operator
      // explicitly asked for this trade in favor of responsive UX.
      //
      // We always dispatch Ok(()): the request's HTTP outcome is
      // irrelevant to the user's intent. The .catch on the fetch
      // just swallows the error so it doesn't surface in console.
      return VEffect(() => {
        const issuer = effectIssuer;
        // Forget the cached user here rather than leaving it to whatever
        // navigation the app does next: signing out is the moment the
        // answer to "who is this" changes, so it is the moment to drop it.
        if (globalThis.__marAuthForget) globalThis.__marAuthForget();
        fetch('/_auth/logout', {
          method: 'POST',
          credentials: 'same-origin',
        }).catch(() => { /* fire-and-forget: server-side cleanup is best-effort */ });
        const msg = apply(toMsg, VCtor('Ok', [VUnit()]));
        deliverFrom(issuer, msg);
      }, 'authLogout');
    }));
    def('Auth.logout', envLookup(env, 'authLogout'));

    // Auth.me with the call site's user type, when the checker knew it. The
    // shape is optional so an older bundle -- one serialized before the auth
    // port had a shape at all -- still runs, unchecked, exactly as it did.
    authMeWithShape = (shape) => native(1, ([toMsg]) => authMeEffect(toMsg, shape));

    def('authMe', native(1, ([toMsg]) => authMeEffect(toMsg, null)));

    function authMeEffect(toMsg, shape) {
      // GET /_auth/whoami: Maybe user.
      return VEffect(() => {
        const issuer = effectIssuer;
        fetch('/_auth/whoami', { credentials: 'same-origin' })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t })))
          .then(r => {
            if (!r.ok) {
              const msg = apply(toMsg, VCtor('Err', [VString('HTTP ' + r.body)]));
              deliverFrom(issuer, msg);
              return;
            }
            let parsed;
            try {
              const raw = jsonBodyOrThrow(r.body);
              // `null` is a signed-out answer, not a user of the wrong shape,
              // so it never meets the guard.
              parsed = raw == null
                ? VCtor('Nothing', [])
                : VCtor('Just', [shape ? checkedMarUser(jsToMar(raw), shape) : jsToMar(raw)]);
            } catch (e) {
              const text = (e && e.marWire) ? e.message
                : 'decode failed: ' + (e && e.message || e);
              const msg = apply(toMsg, VCtor('Err', [VString(text)]));
              deliverFrom(issuer, msg);
              return;
            }
            const msg = apply(toMsg, VCtor('Ok', [parsed]));
            deliverFrom(issuer, msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            deliverFrom(issuer, msg);
          });
        return VUnit();
      }, 'authMe');
    }
    def('Auth.me', envLookup(env, 'authMe'));

    def('httpPost', native(3, ([url, body, toMsg]) => {
      return VEffect(() => {
        const issuer = effectIssuer;
        fetch(url.s, { method: 'POST', body: body.s })
          .then(r => r.text().then(t => ({ ok: r.ok, body: t, status: r.status })))
          .then(r => {
            const result = r.ok
              ? VCtor('Ok', [VString(r.body)])
              : VCtor('Err', [VString(decodeServerError(r.body) || ('HTTP ' + (r.status || 0)))]);
            const msg = apply(toMsg, result);
            deliverFrom(issuer, msg);
          })
          .catch(err => {
            const msg = apply(toMsg, VCtor('Err', [VString(friendlyFetchError(err))]));
            deliverFrom(issuer, msg);
          });
        return VUnit();
      }, 'httpPost');
    }));
    def('Http.post', envLookup(env, 'httpPost'));

    return env;
  }

  // ---------- DOM rendering ----------
  //
  // Two-phase: createDOM builds a fresh element from a VView; patchDOM
  // diffs an existing DOM node against a new VView and applies the
  // minimal mutations.
  //
  // Listeners use the indirect-view trick: each interactive element gets
  // its current VView stashed at `node.__marView`. The listener reads the
  // latest msg from there, so on patch we can update the view reference
  // without removing/re-adding listeners.

  function setMarView(node, view) {
    node.__marView = view;
  }

  // Attach tap dispatch once. Uses node.__marView so the closure reads the
  // latest view after subsequent patches. Respects the `disabled` attr
  // (so a re-render that toggled the flag suppresses the tap even though the
  // listeners stay bound).
  //
  // Mouse and keyboard go through `click` (mouse has no iOS quirk; keyboard
  // Enter/Space arrive as a synthetic `click` with no pointer events). But a
  // TOUCH/pen tap completes on `pointerup` instead: on iOS Safari, a DOM
  // mutation between touchstart and the synthetic click: e.g. a page that
  // re-renders on a per-second countdown tick: makes Safari CANCEL the
  // click, so a tap landing on the same instant as a re-render is silently
  // dropped (the button flashes its tap-highlight, nothing fires).
  // `pointerup` fires regardless of concurrent DOM mutation, so it's immune;
  // we then swallow the click Safari synthesizes next so we don't double-fire.
  function attachClickDispatcher(node) {
    // Dispatch the node's current msg, honoring disabled. Returns whether it
    // actually fired (so the pointer path knows whether to swallow the click).
    const fire = () => {
      const v = node.__marView;
      if (!currentDispatch || !v || v.msg == null) return false;
      if (isDisabled(v)) return false;
      currentDispatch(v.msg);
      return true;
    };

    // Touch/pen: arm on pointerdown, complete on pointerup, but only for a
    // real tap (started on this node, moved < ~10px so it wasn't a scroll).
    let armed = false, downId = -1, downX = 0, downY = 0;
    node.addEventListener('pointerdown', (ev) => {
      if (ev.pointerType === 'mouse') return;      // mouse keeps the click path
      armed = true; downId = ev.pointerId;
      downX = ev.clientX; downY = ev.clientY;
    });
    node.addEventListener('pointerup', (ev) => {
      if (ev.pointerType === 'mouse') return;
      if (!armed || ev.pointerId !== downId) return;
      armed = false;
      // A finger that drifted was a scroll/drag, not a tap.
      if (Math.abs(ev.clientX - downX) > 10 || Math.abs(ev.clientY - downY) > 10) return;
      if (fire()) {
        // Swallow the click iOS synthesizes right after. Self-expiring so
        // that if the click never arrives we never eat a later, real click.
        node.__marSwallowClickUntil = Date.now() + 700;
      }
    });
    // iOS fires pointercancel when it reclassifies the gesture as a scroll.
    node.addEventListener('pointercancel', () => { armed = false; });

    node.addEventListener('click', (ev) => {
      ev.preventDefault();
      if (node.__marSwallowClickUntil && Date.now() < node.__marSwallowClickUntil) {
        node.__marSwallowClickUntil = 0;   // already dispatched on pointerup
        return;
      }
      fire();
    });
  }

  // getAttr reads a named attribute's value from a view, returning
  // null when absent. Generic helper for attrs whose value is a
  // record / object (not just a primitive). Used by onMove which
  // packs { editing, handler } into the attr value.
  function getAttrRaw(view, name) {
    if (!view || !view.attrs) return null;
    for (const a of view.attrs) {
      if (a.name === name) return a.value;
    }
    return null;
  }

  // dispatchOnMove applies a Mar `(Int -> Int -> msg)` handler to
  // the (from, to) indices and pushes the resulting Msg through
  // the current MVU dispatcher.
  //
  // Used by the list reorder gesture: both the mouse/touch drag
  // (attachListReorderDrag) and the keyboard grab
  // (attachListReorderKeyboard, below).
  function dispatchOnMove(handler, from, to) {
    if (!handler || from === to || !currentDispatch) return;
    try {
      const partial = apply(handler, VInt(from));
      const msg = apply(partial, VInt(to));
      currentDispatch(msg);
    } catch (e) {
      console.error('[mar] onMove dispatch failed:', e);
    }
  }

  // attachListReorderDrag wires up the mouse/touch drag gesture on a
  // `mar-list` element in edit mode.
  //
  // Behavior:
  //   - Each child row gets a drag handle `<button.mar-drag-handle>`
  //     prepended on the right side (added in createDOM when the
  //     list has an onMove attr with editing=true).
  //   - Pointer-down on a handle "lifts" the row (CSS class for
  //     shadow + elevation), records starting index.
  //   - Pointer-move calculates which row the cursor is currently
  //     over (by comparing Y position to each row's mid-point),
  //     showing a thin blue drop indicator line above that row.
  //   - Pointer-up: if the cursor is over a different row,
  //     dispatch onMove(start, target); reset all visual state.
  //   - Escape key or pointer-cancel: cancel without dispatching.
  //
  // Realtime visual reorder: as the cursor moves over rows, those
  // rows shift in place to "open up" the drop slot: no separate
  // drop indicator needed (the visible gap IS the indicator). This
  // mirrors iOS table-view drag where the list reflows continuously
  // under the finger rather than waiting for the release.
  function attachListReorderDrag(listEl, handler) {
    let dragging = null;
    const rows = () => Array.from(listEl.children).filter(c =>
      !c.classList.contains('mar-drop-indicator') &&
      !c.classList.contains('mar-live-region'));

    function onPointerDown(ev) {
      // Only react to the drag handle, not the whole row: clicking
      // text or other content shouldn't start a drag.
      const handle = ev.target.closest('.mar-drag-handle');
      if (!handle) return;
      const rowEl = handle.closest('.mar-section-body > *');
      if (!rowEl || rowEl.parentNode !== listEl) return;

      ev.preventDefault();
      const rs = rows();
      const startIdx = rs.indexOf(rowEl);
      if (startIdx < 0) return;

      // Attach pointermove/up/cancel listeners on DOCUMENT (not
      // listEl) and DON'T use setPointerCapture. This is the
      // "modern drag pattern" used by dnd-kit / react-dnd / etc.:
      //
      //   - Document-level listeners receive pointer events
      //     anywhere on the page, so the user can drag past the
      //     section's bounds without us losing the events. Same
      //     benefit setPointerCapture would give, but without the
      //     capture's iOS Safari side effects (a long-debugged
      //     class of bugs where the gesture state machine doesn't
      //     fully release on pointerup, leaving the page in a
      //     "next tap is consumed" state: operator reported
      //     EXACTLY this symptom after every drag: "any button
      //     needs to be tapped twice; tapping somewhere first
      //     releases it").
      //
      //   - Listeners are added per-drag and removed on
      //     pointerup/cancel. No state outlives the drag. The
      //     page is back to clean after release: same as if no
      //     drag ever happened.
      //
      // Bound once here so add/remove use the same function
      // reference; closures over `dragging` capture the same
      // state the previous implementation had.
      document.addEventListener('pointermove', onPointerMove);
      document.addEventListener('pointerup', onPointerUp);
      document.addEventListener('pointercancel', onPointerCancel);

      rowEl.classList.add('mar-row-dragging');
      // The drag-active class on the list enables a CSS transition
      // on non-dragging siblings (transform 200ms ease-out). Setting
      // transforms on them in onPointerMove then animates smoothly
      //: without this class the shifts would snap on each cursor
      // tick and feel jittery. See the CSS rule for the full pair.
      listEl.classList.add('mar-section-drag-active');

      // Snapshot every row's natural geometry BEFORE any shift is
      // applied. We use this snapshot for two things in onPointerMove:
      //
      //   (a) Target-row hit testing. If we used live
      //       getBoundingClientRect, the rects would reflect the
      //       realtime shifts we ourselves applied: moving the
      //       cursor 10px would cause a chain reaction (shift → new
      //       rect → new target → new shift → ...) and the drop
      //       slot would oscillate.
      //
      //   (b) The dragged row's natural height, which is the offset
      //       OTHER rows shift by ("make room for one row"). We
      //       pull it from the snapshot rather than measure live so
      //       the value is stable across the gesture.
      const rowGeometry = rs.map(r => {
        const rect = r.getBoundingClientRect();
        return { el: r, top: rect.top, height: rect.height };
      });

      dragging = {
        rowEl,
        startIdx,
        lastTargetIdx: startIdx,
        pointerId: ev.pointerId,
        startPointerY: ev.clientY,
        rowGeometry,
      };
    }

    function onPointerMove(ev) {
      if (!dragging) return;
      ev.preventDefault();

      // Translate the dragged row to follow the cursor. The delta is
      // relative to where the pointer was at drag-start, so the row
      // moves 1:1 with the finger / mouse. We compose the translate
      // with a tiny scale-up (1.02) for the "picked up" feel that
      // iOS uses on table-view drag: gives clear visual signal that
      // THIS row is the one being moved, beyond the shadow alone.
      const deltaY = ev.clientY - dragging.startPointerY;
      dragging.rowEl.style.transform = 'translateY(' + deltaY + 'px) scale(1.02)';

      // Find the conceptual drop slot using PRE-DRAG geometry (not
      // live rects: see the snapshot rationale in onPointerDown).
      // We do NOT skip the dragging row's entry in geom: including it
      // lets a cursor on the dragging row's own slot resolve to
      // startIdx (no-op), which is the correct UX for "small drag
      // that doesn't cross a neighbor's midpoint". The previous skip
      // caused the loop to leap over startIdx and bias the target
      // one slot toward the cursor direction, producing wrong drops
      // when dragging from the middle.
      //
      // Default = geom.length (the slot AFTER the last row) so a
      // cursor below every midpoint resolves to "drop at the very
      // end". Combined with the effectiveTarget adjustment below,
      // this keeps the end-of-list drop reachable for DOWN drags.
      const y = ev.clientY;
      const geom = dragging.rowGeometry;
      let target = geom.length;
      for (let i = 0; i < geom.length; i++) {
        if (y < geom[i].top + geom[i].height / 2) {
          target = i;
          break;
        }
      }
      // listMove removes `from` first then inserts at `to` in the
      // POST-REMOVAL list. For UP drags (target <= startIdx) the
      // slots above startIdx are unaffected by the removal so target
      // is dispatched as-is. For DOWN drags (target > startIdx)
      // every slot after startIdx shifts up by one when from is
      // removed, so the dispatched index is one less than the
      // conceptual drop slot: without this -1 a drag that should
      // land "above row N" instead lands BELOW row N.
      const startIdx = dragging.startIdx;
      const effectiveTarget =
        target > startIdx ? target - 1 : target;

      // Skip the shift recalc if the resolved target hasn't changed
      // since the last move: the rows are already in the right
      // positions and writing the same inline styles would just
      // churn CSSOM.
      if (effectiveTarget === dragging.lastTargetIdx) return;
      dragging.lastTargetIdx = effectiveTarget;

      // Realtime shifts on OTHER rows. Each row between the dragged
      // row's home slot and the current target slot translates by
      // ±rowHeight to open up the landing space. The combined
      // effect: the user sees the list "reflowing" under their
      // finger, the gap moving with them, no separate drop indicator
      // needed.
      //
      // Indexed against effectiveTarget (not the raw cursor-side
      // target) so the visual gap matches where listMove will
      // actually drop the row. Using raw target would put the gap
      // one row off for DOWN drags.
      const rowHeight =
        geom.find(g => g.el === dragging.rowEl).height;
      for (let i = 0; i < geom.length; i++) {
        const r = geom[i].el;
        if (r === dragging.rowEl) continue;
        let shift = 0;
        if (effectiveTarget > startIdx && i > startIdx && i <= effectiveTarget) {
          shift = -rowHeight;
        } else if (effectiveTarget < startIdx && i >= effectiveTarget && i < startIdx) {
          shift = rowHeight;
        }
        r.style.transform = shift === 0
          ? ''
          : 'translateY(' + shift + 'px)';
      }
    }

    function onPointerUp(ev) {
      if (!dragging) return;
      // No preventDefault here: drop the pointerup default
      // suppression. Modern drag-and-drop libraries (dnd-kit,
      // react-dnd) intentionally don't preventDefault on pointerup
      // because it can interfere with how iOS Safari processes the
      // touch-sequence end (including the synthetic click that
      // should, or shouldn't, fire). The drag itself has already
      // happened; nothing useful to suppress at this point.
      const { startIdx, lastTargetIdx, rowEl, rowGeometry } = dragging;

      // Detach the document-level listeners we attached in
      // onPointerDown. This is the cleanup that pointer-capture
      // would have done implicitly (per spec), but doing it
      // ourselves means no dependency on the browser's release
      // implementation. iOS Safari's broken implicit-release was
      // the original "Done needs 2 taps after drag" bug; this
      // structure sidesteps that entirely.
      document.removeEventListener('pointermove', onPointerMove);
      document.removeEventListener('pointerup', onPointerUp);
      document.removeEventListener('pointercancel', onPointerCancel);


      // FLIP drop animation: slide the dragged row smoothly from
      // where the cursor released it to its final slot in the list,
      // instead of snapping. Without this the row jumps wherever
      // the reorder puts it, which reads as glitchy after the
      // smooth follow-cursor drag.
      //
      // The OTHER rows DO NOT need a FLIP: their drag-time visual
      // positions already match their post-reorder natural
      // positions (the realtime shifts were rehearsing the final
      // layout). So we clear their transforms synchronously and
      // they stay visually still: the dispatch + DOM-reorder +
      // transform-clear all happen in one sync block, so the
      // browser paints a single coherent frame: rows in their new
      // DOM slots, no transforms, no visible motion.
      //
      // Step-by-step (dragged row only):
      //
      //   1. FIRST: snapshot the dragged row's visual position
      //      (cursor pos, with follow-cursor transform applied).
      //   2. Disable transitions on all rows so the cleanups below
      //      don't accidentally animate things we want instant.
      //   3. Clear transforms on all rows. Dragged row briefly
      //      snaps to its OLD-slot natural: invisible because we
      //      stay in the sync block. Other rows stay put visually
      //      (their drag-shift == post-reorder natural).
      //   4. Dispatch reorder → MVU + render moves the dragged row
      //      to its new DOM slot synchronously.
      //   5. LAST: measure the dragged row's NEW natural position.
      //   6. INVERT: re-apply a transform that visually puts the
      //      dragged row back at the cursor's spot.
      //   7. void offsetHeight forces a sync layout so the browser
      //      commits the inverted state before the transition
      //      target is set: without it Chrome may collapse the
      //      next two style writes into a no-op.
      //   8. PLAY: next frame, clear the transform. The transition
      //      we set in step 7 animates from inverted (cursor) to
      //      natural (final slot).

      const cursorRect = rowEl.getBoundingClientRect();

      // Step 2: disable transitions everywhere so the cleanups
      // below don't kick off animations.
      for (const g of rowGeometry) {
        g.el.style.transition = 'none';
      }

      // Step 3: clear all inline transforms. Other rows' visual
      // positions are unaffected (their drag-shifted position ==
      // their post-reorder natural position). The dragged row
      // would visually snap to its OLD slot if the browser painted
      // here, but it doesn't, because we stay sync until step 6.
      for (const g of rowGeometry) {
        g.el.style.transform = '';
      }

      listEl.classList.remove('mar-section-drag-active');
      dragging = null;

      // Step 4: trigger the model update + re-render.
      if (lastTargetIdx !== startIdx) {
        dispatchOnMove(handler, startIdx, lastTargetIdx);
      }

      // Step 5: dragged row is now at its final slot. Measure.
      const finalRect = rowEl.getBoundingClientRect();
      const dx = cursorRect.left - finalRect.left;
      const dy = cursorRect.top - finalRect.top;

      // Restore inline transition='' on other rows so future drags
      // can re-enable the realtime-shift transition via the
      // .mar-section-drag-active class. Leaving inline 'none' on
      // them would override the class rule and break the next
      // drag.
      for (const g of rowGeometry) {
        if (g.el !== rowEl) g.el.style.transition = '';
      }

      // If the dragged row's cursor position already coincides
      // with its final slot (rare: pointer didn't move much),
      // skip the FLIP. Nothing to animate.
      if (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5) {
        rowEl.classList.remove('mar-row-dragging');
        rowEl.style.transition = '';
        rowEl.style.transform = '';
        return;
      }

      // Step 6: invert, put the dragged row back where the
      // cursor was. scale(1.02) lift carries through the
      // animation so the row "lands" by shrinking to natural
      // size + sliding simultaneously (iOS Reminders feel).
      rowEl.style.transform = 'translate(' + dx + 'px, ' + dy + 'px) scale(1.02)';
      // Step 7: force layout flush so the inverted state is
      // committed before we declare the transition.
      void rowEl.offsetHeight;
      rowEl.style.transition = 'transform 260ms cubic-bezier(0.2, 0.9, 0.3, 1)';

      // Step 8: play, clear transform on next frame to trigger
      // the transition. rAF ensures the inverted state was
      // painted at least once before the animation starts.
      requestAnimationFrame(() => {
        rowEl.style.transform = '';
        const cleanup = () => {
          rowEl.classList.remove('mar-row-dragging');
          rowEl.style.transition = '';
          rowEl.style.transform = '';
          rowEl.removeEventListener('transitionend', cleanup);
        };
        rowEl.addEventListener('transitionend', cleanup);
      });
    }

    function onPointerCancel() {
      if (!dragging) return;
      const { rowEl, rowGeometry } = dragging;
      // Detach document-level listeners (mirror of onPointerUp's
      // cleanup): see onPointerDown for the architecture.
      document.removeEventListener('pointermove', onPointerMove);
      document.removeEventListener('pointerup', onPointerUp);
      document.removeEventListener('pointercancel', onPointerCancel);
      rowEl.classList.remove('mar-row-dragging');
      rowEl.style.transform = '';
      rowEl.style.transition = '';
      // Reset shifts on other rows too.
      for (const g of rowGeometry) {
        g.el.style.transform = '';
        g.el.style.transition = '';
      }
      listEl.classList.remove('mar-section-drag-active');
      dragging = null;
    }

    // Only pointerdown stays on listEl: that's the gesture-start
    // detector (needs to fire when the user touches the drag
    // handle inside this section). The move / up / cancel
    // listeners get attached to DOCUMENT inside onPointerDown so
    // they catch events anywhere on the page during the drag, and
    // are detached on pointerup/cancel: no state outlives the
    // gesture (the iOS Safari "stuck capture" bug was caused by
    // listeners + setPointerCapture not unwinding cleanly).
    listEl.addEventListener('pointerdown', onPointerDown);
  }

  // makeDragHandle creates the `≡` handle element prepended to each
  // row in a reorderable list when edit mode is active. Pure DOM
  // factory; the listener attachment is on the LIST element (event
  // delegation): see attachListReorderDrag.
  function makeDragHandle() {
    const h = document.createElement('button');
    h.type = 'button';
    h.className = 'mar-drag-handle';
    h.setAttribute('aria-label', 'Drag to reorder');
    // Three short horizontal bars: iOS-style drag affordance.
    for (let i = 0; i < 3; i++) {
      const bar = document.createElement('span');
      bar.className = 'mar-drag-handle-bar';
      h.appendChild(bar);
    }
    return h;
  }

  // ensureRowEditAffordances guarantees a row inside an editing
  // section carries its drag handle + ARIA wiring, idempotently.
  //
  // Why this exists: the section's createDOM appends a handle to
  // every child during the initial render. BUT keyed reconciliation
  // can create new row DOM via createDOM(rowView) in two places:
  //
  //   (a) patchChildrenKeyed when newKey isn't in oldByKey (a row
  //       added in edit mode, e.g. via the Add Task button), and
  //   (b) patchDOM's tag-mismatch replacement (toggle → hstack on
  //       edit-mode entry, or any future shape-shift)
  //
  //, and neither path goes through the section's createDOM loop,
  // so the fresh row would be missing its handle. Calling this
  // helper from those creation sites (and re-running it on the
  // section's patch path) makes the handle's presence robust to
  // however the row landed in the DOM.
  //
  // Idempotent: bails if a handle is already present, so the cost
  // on the common case (row was created with handle) is one
  // querySelector.
  function ensureRowEditAffordances(rowEl, posIndex, totalCount) {
    if (rowEl.querySelector(':scope > .mar-drag-handle') == null) {
      rowEl.appendChild(makeDragHandle());
    }
    if (!rowEl.hasAttribute('tabindex')) rowEl.setAttribute('tabindex', '0');
    if (!rowEl.hasAttribute('role')) rowEl.setAttribute('role', 'listitem');
    if (!rowEl.hasAttribute('aria-grabbed')) rowEl.setAttribute('aria-grabbed', 'false');
    // posinset / setsize ALWAYS get refreshed: these change on
    // every reorder / insert / delete, and stale values would
    // mislead screen readers.
    rowEl.setAttribute('aria-posinset', String(posIndex + 1));
    rowEl.setAttribute('aria-setsize', String(totalCount));
    // Position-based classes drive the CSS that rounds the focus
    // outline / grabbed bg at the section card's outer corners. Set
    // here (not via CSS :first-child / :last-child) because the
    // section body has non-row children too (mar-live-region for
    // ARIA, mar-drop-indicator during active drag) that would
    // confuse positional selectors. CSS gets a definitive boolean
    // from JS, where we already know the true row count.
    rowEl.classList.toggle('mar-row-first', posIndex === 0);
    rowEl.classList.toggle('mar-row-last', posIndex === totalCount - 1);
  }

  // attachRowDeleteAffordance: appends the per-row delete button
  // (iOS-edit-mode minus circle on the row's left). Only renders
  // when the row is in edit mode: web's normal mode shows just the
  // primary affordance (toggle / link) without a destructive option
  //: the user enters Edit explicitly to do CRUD on the list.
  //
  // We considered the iCloud-Web hover-reveal pattern but it
  // conflicts with apps that have a separate Edit mode (the
  // canonical Mar shape): hovering rows in browse mode then
  // surfaces a destructive action the user hasn't asked for, on
  // top of the toggle they're trying to click. Edit-mode-only is
  // less ambiguous and matches the iOS Mail/Notes/Reminders
  // ergonomics exactly.
  //
  // The click handler dispatches `handler(idx)`: the framework's
  // standard apply-then-dispatch, so the Mar app's `update` sees
  // a `Msg.SomeDelete idx` and decides what to do. Two shapes are
  // common and both must work: delete right away (drop from the
  // model + Service.call to persist), or open a `UI.confirm` and
  // delete only if the user agrees. The tap is a question, not a
  // verdict: see the click handler.
  //
  // Idempotent: re-rendering a row that already has the button
  // reuses the existing DOM node + listener (no stacking). When
  // `editing=false`, any leftover button from a previous render
  // gets removed, so flipping editing mode off cleans up after
  // itself.
  function attachRowDeleteAffordance(rowEl, posIndex, handler, isEditing) {
    let btn = rowEl.querySelector(':scope > .mar-row-delete');
    if (!isEditing) {
      // Browse mode: remove any stale button from a previous
      // render that had editing=true. This is the cleanup leg of
      // toggling Edit → Done while the same DOM node persists.
      if (btn) btn.remove();
      return;
    }
    if (!btn) {
      btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'mar-row-delete';
      btn.setAttribute('aria-label', 'Delete');
      // Minus glyph as SVG so it stays crisp on retina without a
      // font dependency. A 4-unit-tall bar in a 24-unit viewBox
      // renders ~2.3px tall at the 14×14 SVG size: clearly
      // visible against the red disc. The previous 2-unit bar
      // disappeared to roughly 1px on standard DPI screens.
      btn.innerHTML =
        '<svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">' +
        '<rect x="5" y="10" width="14" height="4" rx="2" fill="currentColor"/>' +
        '</svg>';
      btn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        ev.preventDefault();

        // Dispatch immediately, with no motion of our own.
        //
        // This used to play a 340ms slide-and-collapse on the row and
        // dispatch only once it finished, on the assumption that a tap
        // on the delete affordance means the row is leaving. That
        // assumption is wrong for the common destructive pattern, where
        // the handler opens a confirmation dialog instead of deleting.
        // The row collapsed to zero height BEFORE the dialog appeared,
        // and on Cancel the model never changed, so the reconciler saw
        // no diff for that row, and nothing restored the inline styles
        // the animation had left behind (`fill: 'forwards'`). The row
        // stayed invisible until a reload while the server still had it.
        //
        // Whether the row leaves is the app's call, expressed as a
        // model change. An exit animation therefore belongs to the
        // reconciler, driven by the row actually leaving the list, not
        // to this click, which only asks the question.
        try {
          const idxV = VInt(parseInt(btn.dataset.idx || '0', 10));
          const msg = apply(handler, idxV);
          if (currentDispatch) currentDispatch(msg);
        } catch (e) {
          console.error('[mar] onDelete dispatch failed:', e);
        }
      });
      rowEl.appendChild(btn);
    }
    // Refresh per-render: the index shifts when rows above are
    // inserted/removed. Stored on data-idx so the click handler
    // (which closes over `btn` but not the current idx) reads it
    // fresh at click time.
    btn.dataset.idx = String(posIndex);
  }

  // attachListReorderKeyboard: the WAI-ARIA grab + arrow keys
  // pattern, for keyboard / screen-reader users. Invisible to mouse
  // users (no extra UI), discoverable via Tab focus + screen
  // reader announcements.
  //
  // Flow:
  //   1. Each row in edit mode is focusable (tabindex="0",
  //      role="listitem", aria-label="<name>, item N of M").
  //   2. Space or Enter on a focused row → enter "grabbed" state.
  //      Visual focus ring stays; aria-grabbed="true".
  //   3. ArrowUp / ArrowDown while grabbed → reorder live (moves
  //      the DOM node, updates aria-posinset).
  //   4. Space or Enter again → drop (fires onMove from original
  //      idx to current idx).
  //   5. Escape → cancel (revert the live moves, fire nothing).
  //
  // Live position announcements go through a single
  // <div aria-live="polite"> attached to the list.
  function attachListReorderKeyboard(listEl, handler, liveHost) {
    const rows = () => Array.from(listEl.children).filter(c =>
      !c.classList.contains('mar-drop-indicator') &&
      !c.classList.contains('mar-live-region'));

    // One shared live region per list: assistive tech reads
    // updates as polite announcements.
    //
    // Hosted on `liveHost` (the section wrapper), NOT on `listEl`
    // (the section-body), because `.mar-section-body > *` applies
    // padding (10px 0), min-height (22px), and border-bottom (0.5px)
    // to every direct child. Even though the live region is
    // `position: absolute` (out of flow, so it doesn\'t push siblings),
    // appending it as the last child of the body kicks the previous
    // actual last row out of `:last-child`, gaining a hairline
    // border, and visually nudging the section card\'s end by enough
    // to read as a "phantom extra row" the moment Edit is toggled.
    // Hosting on the section wrapper sidesteps all of that: the
    // wrapper has no descendant rules to inherit.
    const host = liveHost || listEl;
    let live = host.querySelector(':scope > .mar-live-region');
    if (!live) {
      live = document.createElement('div');
      live.className = 'mar-live-region';
      live.setAttribute('aria-live', 'polite');
      live.setAttribute('aria-atomic', 'true');
      host.appendChild(live);
    }
    function announce(msg) {
      // Clearing first forces re-announcement when the same text
      // would otherwise repeat (e.g., consecutive arrow-up presses).
      live.textContent = '';
      // setTimeout 0 = next microtask, gives the screen reader
      // time to register the clear before the new text.
      setTimeout(() => { live.textContent = msg; }, 0);
    }

    let grabbed = null; // { rowEl, startIdx }

    // onGrabbedBlur cancels the grab when focus leaves the grabbed
    // row. Without this, pressing Tab while grabbed creates a
    // "zombie" state: the row stays styled as `.mar-row-grabbed`
    // (dark blue fill) AND another row picks up the browser's
    // `:focus-visible` ring (blue outline), two rows look
    // "selected", subsequent Space presses can't resolve which
    // row to act on, and arrow keys move the grabbed (invisible-
    // to-the-user attention) instead of the focused one.
    //
    // We defer the check to a microtask via setTimeout(…, 0):
    // some browsers fire transient blur/focus pairs when a node
    // is moved via insertBefore (the very thing moveGrabbed does
    // on each arrow keypress). By the time the microtask runs,
    // focus has settled. If it landed back on the grabbed row,
    // we keep the grab. If it landed elsewhere, the user
    // intentionally shifted attention: cancel.
    function onGrabbedBlur() {
      setTimeout(() => {
        if (!grabbed) return;
        if (document.activeElement === grabbed.rowEl) return;
        endGrab(false);
      }, 0);
    }

    function startGrab(rowEl) {
      const idx = rows().indexOf(rowEl);
      if (idx < 0) return;
      grabbed = { rowEl, startIdx: idx };
      rowEl.setAttribute('aria-grabbed', 'true');
      rowEl.classList.add('mar-row-grabbed');
      rowEl.addEventListener('blur', onGrabbedBlur);
      announce('Grabbed item ' + (idx + 1) + ' of ' + rows().length + '. Use up and down arrows to move.');
    }

    function endGrab(commit) {
      if (!grabbed) return;
      const { rowEl, startIdx } = grabbed;
      const currentIdx = rows().indexOf(rowEl);
      rowEl.setAttribute('aria-grabbed', 'false');
      rowEl.classList.remove('mar-row-grabbed');
      rowEl.removeEventListener('blur', onGrabbedBlur);
      grabbed = null;
      if (commit && currentIdx !== startIdx) {
        // Fire the handler. The model update will cause a re-
        // render that "officially" places the row; our local DOM
        // moves were just preview.
        dispatchOnMove(handler, startIdx, currentIdx);
        announce('Moved to position ' + (currentIdx + 1) + ' of ' + rows().length + '.');
      } else if (commit) {
        announce('Dropped in place.');
      } else {
        announce('Move cancelled.');
      }
    }

    function moveGrabbed(delta) {
      if (!grabbed) return;
      const rs = rows();
      const idx = rs.indexOf(grabbed.rowEl);
      const target = Math.max(0, Math.min(rs.length - 1, idx + delta));
      if (target === idx) return;
      // Move DOM live: gives instant visual + screen-reader
      // feedback. The final model update happens on drop.
      const anchor = (delta > 0)
        ? (rs[target].nextSibling || null)
        : rs[target];
      listEl.insertBefore(grabbed.rowEl, anchor);
      // Refresh position-driven classes + aria attrs on EVERY row,
      // not just the grabbed one. `mar-row-first` / `mar-row-last`
      // drive the rounded-corner border-radius (18px at the card's
      // outer corners vs 8px between rows). insertBefore moves the
      // grabbed row but leaves these classes stale on it AND on the
      // rows it traded places with, so the focused outline shows
      // an 8px corner where the card has an 18px curve, visible as
      // a "broken" ring at the top or bottom row.
      const afterMove = rows();
      afterMove.forEach((r, i) => {
        r.classList.toggle('mar-row-first', i === 0);
        r.classList.toggle('mar-row-last', i === afterMove.length - 1);
        r.setAttribute('aria-posinset', String(i + 1));
        r.setAttribute('aria-setsize', String(afterMove.length));
      });
      // Restore focus to the moved row. Browsers vary: some
      // preserve focus across insertBefore on the active element,
      // some momentarily transfer it to `<body>` and the user-
      // agent never moves it back. In the latter case our
      // onGrabbedBlur handler sees `activeElement !== grabbed.rowEl`
      // and cancels the grab, so after one arrow press, the next
      // arrow / Space silently does nothing (the symptom the user
      // saw: "ArrowDown worked, ArrowUp did nothing"). Explicitly
      // re-focusing right after the move makes the gesture stable
      // across all browsers.
      grabbed.rowEl.focus();
      announce('Position ' + (target + 1) + ' of ' + afterMove.length + '.');
    }

    listEl.addEventListener('keydown', (ev) => {
      const rowEl = ev.target.closest('.mar-section-body > *');
      if (!rowEl || rowEl.parentNode !== listEl) return;
      if (rowEl.classList.contains('mar-drop-indicator') ||
          rowEl.classList.contains('mar-live-region')) return;

      switch (ev.key) {
        case ' ':
        case 'Enter':
          ev.preventDefault();
          if (grabbed) {
            // Commit the drop regardless of which row currently
            // has focus. The blur handler above should have
            // already ended the grab if focus left the grabbed
            // row: this is a defensive fallback for browsers /
            // edge cases where the blur didn't fire (e.g.,
            // focus moved via JS rather than user interaction).
            endGrab(true);
          } else {
            startGrab(rowEl);
          }
          break;
        case 'ArrowUp':
          if (grabbed) { ev.preventDefault(); moveGrabbed(-1); }
          break;
        case 'ArrowDown':
          if (grabbed) { ev.preventDefault(); moveGrabbed(1); }
          break;
        case 'Escape':
          if (grabbed) {
            ev.preventDefault();
            // Revert the live moves by putting the row back at
            // its starting index.
            const rs = rows();
            const currentIdx = rs.indexOf(grabbed.rowEl);
            if (currentIdx !== grabbed.startIdx) {
              const anchor = rs[grabbed.startIdx] || null;
              listEl.insertBefore(grabbed.rowEl, anchor);
            }
            endGrab(false);
          }
          break;
      }
    });
  }

  // Reads the `disabled` attr off a view; returns false when absent
  // or when the value isn't `true`. Renderer-side guard for buttons.
  function isDisabled(view) {
    if (!view || !view.attrs) return false;
    for (const a of view.attrs) {
      if (a.name === 'disabled' && a.value && a.value.k === 'B' && a.value.b) {
        return true;
      }
    }
    return false;
  }

  // Sync the DOM `disabled` property with the view's `disabled` attr.
  // Setting the property (not just the attribute) is what suppresses
  // hover/focus styling and makes the browser skip the click event.
  function applyDisabledAttr(node, view) {
    node.disabled = isDisabled(view);
  }

  // Sync disabled state onto an <a>, which doesn't have a native
  // `disabled` property. We set aria-disabled (assistive tech +
  // [aria-disabled=true] CSS selector hook) and toggle a class so
  // the stylesheet can fade the row + kill pointer events. The
  // delegated link-click handler additionally consults isDisabled()
  // before navigating, so keyboard activation (Enter on a focused
  // link) is also blocked.
  function applyAnchorDisabled(node, view) {
    const disabled = isDisabled(view);
    if (disabled) {
      node.setAttribute('aria-disabled', 'true');
      node.classList.add('mar-disabled');
      // Pull the link out of tab order while disabled. Without
      // this, Tab still lands on it (because we force tabindex=0
      // on every navigationLink), and Enter activates the
      // navigation: defeating the disabled state for keyboard
      // users. Returns to tabindex=0 when re-enabled below.
      node.setAttribute('tabindex', '-1');
    } else {
      node.removeAttribute('aria-disabled');
      node.classList.remove('mar-disabled');
      node.setAttribute('tabindex', '0');
    }
  }

  // applyInputKind reads the input-kind flag attrs (email / password
  // / newPassword / numeric) and translates them into the right HTML
  // attributes. Defaults to type=text when no kind attr is present.
  // Multiple input-kind attrs on one input: last one wins; the user
  // shouldn't combine them anyway.
  function applyInputKind(node, view) {
    let type = 'text';
    let autocomplete = null;
    let inputmode = null;
    let pattern = null;
    if (view.attrs) {
      for (const a of view.attrs) {
        switch (a.name) {
          case 'inputKindEmail':
            type = 'email'; autocomplete = 'email'; inputmode = 'email';
            break;
          case 'inputKindPassword':
            type = 'password'; autocomplete = 'current-password';
            break;
          case 'inputKindNewPassword':
            type = 'password'; autocomplete = 'new-password';
            break;
          case 'inputKindNumeric':
            inputmode = 'numeric'; pattern = '[0-9]*';
            break;
          case 'inputKindOneTimeCode':
            // `autocomplete="one-time-code"` activates iOS Mail's
            // "Code from Mail" autofill, when the user receives an
            // email with a recent code, Safari surfaces it as a
            // keyboard suggestion. Chrome's "Smart Lock" does the
            // same thing. Orthogonal to `UI.numeric` so a real OTP
            // field uses both: `[ numeric, oneTimeCode ]`.
            autocomplete = 'one-time-code';
            break;
          case 'inputKindNumericCode':
            // Convenience: bundles inputKindNumeric + inputKindOneTimeCode.
            // The UI.numericCode attr emits this single flag; the
            // renderer expands it. Matches the typical OTP / 2FA case
            // (numeric keypad on mobile + Code-from-Mail autofill).
            inputmode = 'numeric'; pattern = '[0-9]*';
            autocomplete = 'one-time-code';
            break;
        }
      }
    }
    // Only set type for <input>; <textarea> ignores it.
    if (node.tagName === 'INPUT') node.type = type;
    if (autocomplete) node.setAttribute('autocomplete', autocomplete);
    if (inputmode) node.setAttribute('inputmode', inputmode);
    if (pattern) node.setAttribute('pattern', pattern);
  }

  function attachInputDispatcher(node) {
    node.addEventListener('input', (ev) => {
      const v = node.__marView;
      if (!currentDispatch || !v || v.msg == null) return;
      currentDispatch(apply(v.msg, VString(node.value)));
    });
  }

  // attachSubmitDispatcher wires the UI.submit attr (if present) to
  // Enter on text inputs.
  function attachSubmitDispatcher(node) {
    node.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Enter') return;
      const v = node.__marView;
      if (!currentDispatch || !v || !v.attrs) return;
      const submitAttr = v.attrs.find(a => a.name === 'submit');
      if (!submitAttr) return;
      // Textareas: only Cmd/Ctrl+Enter so the user can type newlines
      // freely. Inputs: any Enter.
      if (node.tagName === 'TEXTAREA' && !ev.metaKey && !ev.ctrlKey) return;
      ev.preventDefault();
      currentDispatch(submitAttr.value);
    });
  }

  function getAttr(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a ? a.value.s : '';
  }

  // applySizing reads `width` / `height` attrs (records with __unit
  // and amount fields, built by UI.chars / UI.lines) and applies them
  // to the DOM element. Both attrs are optional: sizing without them
  // falls back to the input's default (full-width, browser/CSS
  // default height).
  //
  // Padding constants here MUST stay in sync with the .mar-textfield
  // CSS rule (10px 14px) so the calc() math lands on a width that
  // actually fits N character columns visually.
  function applySizing(node, view) {
    const w = readLengthAttr(view, 'width');
    if (w && w.unit === 'chars') {
      // The width budget has to cover four things, in order:
      //
      //   - 28px horizontal padding (14px × 2 from .mar-textfield)
      //   - 2px border (1px × 2)
      //   - 2-4px slack for the caret + browser-specific scroll
      //     padding (Safari adds a few px when the caret sits at the
      //     trailing edge so it doesn't get clipped by the border)
      //   - the actual N×ch content area
      //
      // box-sizing on .mar-textfield is `border-box`, so max-width
      // INCLUDES padding + border: we have to bake them back in or
      // the content area ends up ~Nch - 30px wide instead of Nch.
      // Total constant: 28 + 2 + 4 = 34px.
      //
      // The accompanying `font-variant-numeric: tabular-nums` rule
      // in the CSS makes every digit (0-9) render at exactly `1ch`
      // wide. Without it, "888888" is ~5% wider than "111111" in
      // proportional fonts (system-ui, SF Pro), causing horizontal
      // scroll-flicker when the cursor passes near the trailing
      // edge of code/numeric inputs.
      node.style.maxWidth = 'calc(' + w.amount + 'ch + 34px)';
      // Opt the input out of the hstack flex:1 stretch rule so a
      // sized field beside a button doesn't get re-expanded by the
      // flex layout. `0 0 auto` = don't grow, don't shrink, use the
      // declared (max-)width.
      node.style.flex = '0 0 auto';
    }
    const h = readLengthAttr(view, 'height');
    if (h && h.unit === 'lines') {
      if (node.tagName === 'TEXTAREA') {
        // For <textarea>, the HTML-native `rows` attribute is the
        // right way to set initial height in lines. The user can
        // still drag the resize handle to grow it further (CSS rule
        // `resize: vertical` is preserved).
        node.setAttribute('rows', String(h.amount));
      }
    }
  }

  // applyImageAttrs reads image-only attrs and applies them to an
  // <img>: `size` (fixed width + height, each a px length-value built
  // by UI.size (px w) (px h)) and the content-mode flags (fit/fill →
  // object-fit contain/cover). All optional. Without `size` the image
  // fills its container width and keeps its aspect ratio (the
  // .mar-image CSS default). Default content mode is contain (no crop).
  function applyImageAttrs(node, view) {
    const sz = getAttrRaw(view, 'size');
    if (sz && sz.k === 'R' && sz.fields) {
      const w = readPxAmount(sz.fields.w);
      const h = readPxAmount(sz.fields.h);
      if (w != null) { node.style.width = w + 'px'; node.style.maxWidth = 'none'; }
      if (h != null) node.style.height = h + 'px';
    }
    if (getAttrRaw(view, 'contentModeCover')) node.style.objectFit = 'cover';
    else if (getAttrRaw(view, 'contentModeFit')) node.style.objectFit = 'contain';
  }

  // readPxAmount unwraps a px length-value record ({__unit:'px',
  // amount:N}) to its integer N, or null if malformed.
  function readPxAmount(v) {
    if (v && v.k === 'R' && v.fields && v.fields.amount && v.fields.amount.k === 'I') {
      return v.fields.amount.n;
    }
    return null;
  }

  // applyLayoutAttrs applies the UNIVERSAL sizing attrs: width /
  // height carrying chars / lines / fill Size values: to any view's
  // node. Runs from createDOM's shared tail and the patch path, so
  // swapping `width fill` ↔ `width (chars 12)` between renders stays
  // in sync (classList.toggle + unconditional style writes make it
  // idempotent).
  //
  // `fill` is contextual: the .mar-w-fill / .mar-h-fill classes
  // resolve against the parent's flex direction (main axis → grow,
  // cross axis → stretch), so the class IS the mechanism. chars /
  // lines become inline styles.
  //
  // Tags with their own sizing pipelines opt out of the style half:
  // inputs (textField / textArea / picker) keep applySizing's
  // padding-aware max-width + rows semantics, images size via the
  // px-based `size` attr. The fill classes still apply to them.
  function applyLayoutAttrs(node, view) {
    const w = readLengthAttr(view, 'width');
    const h = readLengthAttr(view, 'height');
    node.classList.toggle('mar-w-fill', !!(w && w.unit === 'fill'));
    node.classList.toggle('mar-h-fill', !!(h && h.unit === 'fill'));
    const ownSizing = view.tag === 'image' || view.tag === 'textField'
      || view.tag === 'textArea' || view.tag === 'picker' || view.tag === 'datePicker';
    if (ownSizing) return;
    node.style.width = (w && w.unit === 'chars') ? w.amount + 'ch' : '';
    node.style.height = (h && h.unit === 'lines') ? h.amount + 'lh' : '';
  }

  // applyAlignAttr maps a stack's `align` attr onto its cross axis
  // (align-items). Each stack honors only its own axis's values plus
  // center, vstack: leading/center/trailing, hstack: top/center/
  // bottom; a wrong-axis value is ignored rather than guessed. No
  // attr → the base-class default (vstack stretch, hstack center).
  // Align only places HUGGING children: a child with the matching
  // fill has no cross-axis slack (the per-child .mar-*-fill stretch
  // rule wins), so align is pure position, never size.
  function applyAlignAttr(node, view) {
    const wanted = getAttr(view, 'align');
    const valid = view.tag === 'vstack'
      ? ['leading', 'center', 'trailing']
      : ['top', 'center', 'bottom'];
    for (const v of ['leading', 'center', 'trailing', 'top', 'bottom']) {
      node.classList.toggle('mar-align-' + v, wanted === v && valid.includes(v));
    }
  }

  // readLengthAttr unwraps a sizing-attr value into { unit, amount }.
  // The Mar-side type system enforces the axis: `width` carries a
  // Size Width (chars N / fill), `height` a Size Height (lines N /
  // fill), so the unit check here is defensive; a malformed value
  // just returns null.
  function readLengthAttr(view, name) {
    if (!view.attrs) return null;
    for (const a of view.attrs) {
      if (a.name !== name) continue;
      const v = a.value;
      if (v && v.k === 'R' && v.fields) {
        const u = v.fields.__unit;
        const amt = v.fields.amount;
        if (u && u.k === 'S' && amt && amt.k === 'I') {
          return { unit: u.s, amount: amt.n };
        }
      }
      return null;
    }
    return null;
  }

  // toggleIsOn reads the `isOn` Bool attr off a UI.toggle view.
  // Defaults to false when missing so the DOM has a deterministic
  // starting state.
  function toggleIsOn(view) {
    const a = view.attrs && view.attrs.find(a => a.name === 'isOn');
    return !!(a && a.value && a.value.b);
  }

  // renderPickerOptions rebuilds the <option> children of a picker's
  // <select> from the view's `options` / `selected` / `toLabel` attrs.
  // Used by both createDOM (initial mount) and patchDOM (when the
  // user's update swaps the model's options or selected value).
  // Clears the select first so the patch path doesn't accumulate
  // stale entries when an option is removed.
  function renderPickerOptions(select, view) {
    while (select.firstChild) select.removeChild(select.firstChild);
    const optionsAttr = view.attrs.find(a => a.name === 'options');
    const selectedAttr = view.attrs.find(a => a.name === 'selected');
    const toLabelAttr = view.attrs.find(a => a.name === 'toLabel');
    if (!optionsAttr || !optionsAttr.value || optionsAttr.value.k !== 'L') return;
    if (!toLabelAttr || !toLabelAttr.value || toLabelAttr.value.k !== 'Fn') return;
    const options = optionsAttr.value.xs;
    const selected = selectedAttr && selectedAttr.value;
    let selectedIdx = -1;
    for (let i = 0; i < options.length; i++) {
      const opt = document.createElement('option');
      opt.value = String(i);
      const labelV = apply(toLabelAttr.value, options[i]);
      opt.textContent = (labelV && labelV.k === 'S') ? labelV.s : String(labelV);
      select.appendChild(opt);
      if (selected !== undefined && eqValues(options[i], selected)) {
        selectedIdx = i;
      }
    }
    // Set the select's value *after* options are appended: assigning
    // before they exist silently fails and the dropdown shows the
    // wrong default. -1 (no match) leaves the browser's default of
    // index 0 in place, which is the safest fallback when the model's
    // selected value isn't in the option list.
    if (selectedIdx >= 0) select.value = String(selectedIdx);
  }

  // buildNavigationChrome pulls navigationTitle / trailing / leading
  // attrs off a navigationStack view and returns the TWO chrome
  // nodes that precede the nav body, in slot order:
  //
  //   [0] <div.mar-nav-toolbar-row>  ← the PINNED bar. Auto-back /
  //       leading on the left, trailing on the right (glass pills,
  //       iOS 26 "Liquid Glass" toolbar pattern), and the inline
  //       title in the center, which fades in once the large title
  //       scrolls out from under it.
  //   [1] <header.mar-nav-bar>       ← the 32px bold large title.
  //
  // This mirrors iOS large-title navigation: the bar is pinned on
  // screen (position: sticky in CSS, back button and actions never
  // scroll away), while the large title is CONTENT and scrolls with
  // the page. When the large title disappears under the bar, the
  // bar's small centered title takes over: the same handoff iOS
  // does. Both nodes are always returned (display:none when empty)
  // so the create and patch paths can address slots positionally.
  function buildNavigationChrome(view) {
    const titleAttr = view.attrs && view.attrs.find(a => a.name === 'navigationTitle');
    const trailingAttr = view.attrs && view.attrs.find(a => a.name === 'topBarTrailing');
    const leadingAttr  = view.attrs && view.attrs.find(a => a.name === 'topBarLeading');
    const depth = currentNavDepth();
    // Back chevron is framework-owned and independent of any
    // user-provided topBarLeading content: it appears whenever
    // there's a previous page to return to. SwiftUI works the same
    // way: .navigationBarBackButtonHidden(true) is the explicit
    // opt-out, not the default consequence of setting toolbar
    // items. Without this, a sub-page that uses topBarLeading for
    // a custom button (Edit, Filter, ...) silently loses the
    // ability to navigate back, which was the bug that motivated
    // this whole refactor.
    const autoBack = depth > 0;
    const hasButtons = autoBack || !!leadingAttr || !!trailingAttr;

    // Slot 0: pinned toolbar row.
    const toolbarRow = document.createElement('div');
    toolbarRow.className = 'mar-nav-toolbar-row';
    if (!hasButtons && !titleAttr) {
      toolbarRow.style.display = 'none';
    } else {
      const left = document.createElement('div');
      left.className = 'mar-nav-side';
      // Order matters: back chevron first (closest to the leading
      // edge of the screen), custom topBarLeading after it. This
      // mirrors SwiftUI's automatic placement: toolbar items go
      // BESIDES the auto-injected back button, not in place of it.
      if (autoBack) {
        left.appendChild(buildBackButton());
      }
      if (leadingAttr) {
        left.appendChild(createDOM(leadingAttr.value));
      }
      toolbarRow.appendChild(left);

      // Inline title: hidden until the large title scrolls out
      // (wireNavInlineTitle toggles .mar-nav-scrolled). The large
      // title is the page's real heading; this is a visual echo,
      // so hide it from the accessibility tree.
      if (titleAttr) {
        const inline = document.createElement('div');
        inline.className = 'mar-nav-inline-title';
        inline.textContent = titleAttr.value.s;
        inline.setAttribute('aria-hidden', 'true');
        toolbarRow.appendChild(inline);
      }

      const right = document.createElement('div');
      right.className = 'mar-nav-side mar-nav-side-trailing';
      if (trailingAttr) right.appendChild(createDOM(trailingAttr.value));
      toolbarRow.appendChild(right);

      if (!hasButtons) {
        // Title-only page: the bar reserves no space at rest, so
        // the large title keeps its position; only the inline
        // title floats in once the page scrolls.
        toolbarRow.classList.add('mar-nav-toolbar-bare');
      } else if (!titleAttr) {
        // Buttons-only page: the row is the entire chrome, so it
        // owns the 24px gap to the body that the large-title
        // header normally provides.
        toolbarRow.classList.add('mar-nav-toolbar-solo');
      }
    }

    // Slot 1: large title. Scrolls away with the content, like iOS.
    const header = document.createElement('header');
    header.className = 'mar-nav-bar';
    if (titleAttr) {
      const titleRow = document.createElement('div');
      titleRow.className = 'mar-nav-title-row';
      const titleEl = document.createElement('div');
      titleEl.className = 'mar-nav-title';
      titleEl.textContent = titleAttr.value.s;
      titleRow.appendChild(titleEl);
      header.appendChild(titleRow);
    } else {
      header.style.display = 'none';
    }

    wireNavInlineTitle(toolbarRow, titleAttr ? header : null);
    return [toolbarRow, header];
  }

  // wireNavInlineTitle drives the large-title → inline-title
  // handoff: when the large-title header scrolls out of the strip
  // the pinned bar occupies, the bar's centered title fades in;
  // scroll back to the top and it fades out again. One observer per
  // chrome build, stored on the row: the patch path disconnects
  // the old row's observer explicitly, and rows dropped by full
  // page navigation take theirs to the garbage collector.
  function wireNavInlineTitle(toolbarRow, header) {
    if (!header || typeof IntersectionObserver === 'undefined') return;
    // Shrink the viewport's top by the strip the pinned bar
    // occupies, so the title only counts as "gone" once it has
    // fully slid under where the bar sits. The strip depends on
    // the bar's resting geometry: with buttons the row is 36px
    // tall below the stack's 24px top padding; on a bare
    // (title-only) page the row is zero-height, the title rests
    // at 24px, and using the larger strip would swallow the title
    // at rest and show the inline pill before any scrolling.
    const bare = toolbarRow.classList.contains('mar-nav-toolbar-bare');
    const obs = new IntersectionObserver(([entry]) => {
      // One class, one consumer. It used to be mirrored onto the stack as
      // well, because the old progressive blur needed more pseudo-elements
      // than a single element has; the bar is one opaque surface now, so
      // the row's own ::after is the only thing listening.
      toolbarRow.classList.toggle('mar-nav-scrolled', !entry.isIntersecting);
    }, {
      rootMargin: (bare ? '-28px' : '-64px') + ' 0px 0px 0px',
    });
    obs.observe(header);
    toolbarRow._marNavObserver = obs;
  }

  // buildBackButton returns the iOS 26-style circular glass pill
  // with just the chevron inside. No visible text: the previous
  // screen\'s title goes into the `title` attribute as a hover
  // tooltip + aria-label so the affordance is accessible without
  // taking horizontal space in the toolbar.
  function buildBackButton() {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'mar-nav-back';
    const prev = (history.state && history.state.prevTitle) || '';
    btn.setAttribute('aria-label', prev ? 'Back to ' + prev : 'Back');
    if (prev) btn.setAttribute('title', 'Back to ' + prev);
    const chev = document.createElement('span');
    chev.className = 'mar-nav-back-chev';
    chev.textContent = '‹';
    btn.appendChild(chev);
    btn.addEventListener('click', () => {
      // Native browser back: fires popstate, which calls render()
      // with the previous URL + depth, triggering the "back"
      // view-transition direction.
      history.back();
    });
    return btn;
  }

  // currentNavDepth reads our SPA-internal "how deep into the
  // navigation stack are we" counter, stamped onto history.state
  // by every push / replace. Returns 0 when the entry has no state
  // yet (e.g. the very first page load before mountPages runs the
  // initial replace).
  // Every history entry the app creates carries an id (history.state.marEntry),
  // and a page's model belongs to its entry, not to its route: A, B, then A
  // again is two models for A, and Back from the second A to B to the first A
  // finds the first one as it was left. A UI.sheet's own history entry copies
  // the id of the page under it: it is the same screen. The boot stamp keeps
  // ids unique across a full reload, whose entries keep the ids they had.
  const entryBoot = Date.now().toString(36);
  let entrySeq = 0;
  function newEntryId() { return entryBoot + '.' + (++entrySeq); }
  function curEntryId() {
    return (history.state && history.state.marEntry) || 'e0';
  }
  function currentNavDepth() {
    return (history.state && typeof history.state.marDepth === 'number')
      ? history.state.marDepth
      : 0;
  }

  // Respect the OS-level "reduce motion" accessibility preference.
  // Users who set this don't want page-transition slide animations
  //: disable them entirely and just do the DOM swap.
  function prefersReducedMotion() {
    return window.matchMedia
      && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  }

  // ensureUIStyles injects the CSS for the UI.* vocabulary once. The
  // styling aims for the iOS Form/List card-list look on web: light
  // gray page background, white sections with rounded corners, gray
  // section headers, dividers between rows.
  function ensureUIStyles() {
    // Always replace the existing <style> with the current build's
    // content. Hot reload re-runs the IIFE but document.head's
    // <style> element survives the page lifecycle: without removing
    // and re-appending, edits to this CSS string wouldn't show up
    // in the browser until the user did a full reload.
    const old = document.getElementById('mar-ui-style');
    if (old) old.remove();
    const style = document.createElement('style');
    style.id = 'mar-ui-style';
    // Desktop-first design language. Avoids the iOS-card aesthetic
    // (rounded white cards on gray, big shadows, blue accents) in
    // favor of a flatter, wider layout closer to Linear / Notion /
    // modern dashboards: 960px content column, subtle borders instead
    // of cards, neutral grayscale, system font stack at 15-16px.
    // The same DSL still renders as proper iOS Forms on Swift:
    // only the WEB rendering picks the desktop idiom.
    style.textContent = [
      // System-flavored vocabulary: native system font stack,
      // generous space, pill-shaped buttons, near-black `#1d1d1f`
      // text, link-blue (#0071e3) accents, soft 0.5px dividers.
      // The page reads as content (the notes), not chrome.

      // height: 100% on html, min-height on body so the body's box
      // grows past the viewport when content is taller. Without
      // min-height (just height: 100%), the body stays exactly one
      // viewport tall: anything below the fold shows html's solid
      // background instead of the body's gradient, producing a
      // visible horizontal "seam" partway down long pages.
      'html { margin: 0; padding: 0; height: 100%; }',
      'body { margin: 0; padding: 0; min-height: 100%; }',
      // color-scheme tells the browser the page supports both light
      // and dark UAs. Without it, form-control internals (input /
      // textarea / select default backgrounds, scrollbar tracks,
      // focus rings) pick the LIGHT branch even when the OS is in
      // dark mode: visible as a stark white input on an otherwise
      // dark page. With `light dark` listed, the UA flips its
      // internals to match `prefers-color-scheme`, and our explicit
      // .mar-textfield rules layer over a dark-correct UA default.
      ':root { color-scheme: light dark; }',
      // <html> picks up its own solid background matching the bottom
      // of the body gradient. Two reasons:
      //
      //  1. Overscroll bounce (Safari, mobile in particular) reveals
      //     the html element above/below the body. Without this it
      //     defaults to white: jarring on a dark-mode page that's
      //     otherwise charcoal.
      //
      //  2. The View Transitions API renders its snapshot pair
      //     inside a transient overlay anchored to <html>. While the
      //     two snapshots slide horizontally, the gap between them
      //     briefly shows <html>'s background. Same default-white
      //     story: would flash light during every page transition
      //     in dark mode.
      //
      // The light-mode rule pairs with the @media (prefers-color-
      // scheme: dark) override further down.
      'html { background-color: #f0f0f3; }',
      'body {',
      // Subtle vertical gradient: "Liquid Glass" needs a hint of
      // directional light to look right (glass without context
      // looks like flat translucency). Light at the top fades to
      // a slightly darker tone toward the bottom, so the glass
      // pills and cards above pick up a soft top highlight.
      '  background: linear-gradient(180deg, #f7f7f9 0%, #f5f5f7 55%, #f0f0f3 100%);',
      '  background-attachment: fixed;',
      '  color: #1d1d1f;',
      '  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Display",',
      '    "SF Pro Text", "Helvetica Neue", system-ui, sans-serif;',
      '  font-size: 17px;',
      '  line-height: 1.47;',
      '  -webkit-font-smoothing: antialiased;',
      '  text-rendering: optimizeLegibility;',
      '}',

      // Page container: content stays inside a ~1024px column to
      // keep line lengths readable on wide displays. Big top padding
      // gives the large title breathing room; the page is
      // content-first, no nav chrome on top.
      // iOS-specific notes:
      //
      // 1. Padding uses `max(<min>, env(safe-area-inset-*))` so the
      //    page never renders content under the notch / Dynamic
      //    Island / home indicator. On desktop and Android (where
      //    the env() values are 0), the min wins and layout is
      //    identical to before. On iPhone with notch the top padding
      //    grows to ~44pt so the back button + title clear the
      //    status bar properly.
      //
      // 2. `min-height` is declared twice: 100vh first as the
      //    fallback, 100dvh second so supporting browsers (Safari
      //    15.4+, Chrome 108+, Firefox 110+) use the dynamic value
      //    that tracks URL-bar collapse. With plain 100vh, iOS
      //    Safari reports the FULL viewport (URL bar hidden),
      //    causing content to appear shifted up when the URL bar
      //    is visible.
      '.mar-nav-stack {',
      '  display: block;',
      '  max-width: 1024px;',
      '  width: 100%;',
      '  margin: 0 auto;',
      // No top padding: the sticky toolbar row is the first child and OWNS
      // the safe-area top (it carries env(safe-area-inset-top) as its own
      // padding-top). This is what lets its solid background fill the iOS
      // status-bar strip: see the .mar-nav-toolbar-row note below.
      '  padding-top: 0;',
      '  padding-right: max(32px, env(safe-area-inset-right));',
      '  padding-bottom: max(48px, calc(env(safe-area-inset-bottom) + 24px));',
      '  padding-left: max(32px, env(safe-area-inset-left));',
      '  min-height: 100vh;',
      '  min-height: 100dvh;',
      '  box-sizing: border-box;',
      '}',

      // Large-title header: scrolls away with the content, exactly
      // like iOS. Only the toolbar row below is pinned.
      '.mar-nav-bar {',
      '  margin-bottom: 24px;',
      '}',
      // The toolbar row is a SOLID, full-width sticky bar anchored at top:0:
      // apple.com's exact technique, and the only thing that fills the iOS
      // status-bar strip when the page is scrolled.
      //
      // Why solid + sticky, after three failed floating/fixed attempts: on
      // iOS 26 a `position: fixed` element is inset to the safe area and can
      // NOT reach under the status bar (measured on the simulator: env()
      // inside it even reads 0), so no fixed element or pseudo could ever
      // paint that strip. A `position: sticky` element CAN: pinned at top:0 it
      // reaches the physical screen top; its padding-top holds
      // env(safe-area-inset-top) so the pills sit below the notch while the
      // bar's own background fills the strip; and page content scrolls UNDER
      // it and is hidden. When you scroll off the top iOS drops theme-color
      // and lets content flow edge-to-edge under the status bar: EVERY site
      // does this (Wikipedia included), and a solid sticky header is how
      // apple.com covers it too.
      //
      // The trade vs. the old floating-pills look: the bar is a flat opaque
      // surface, not translucent glass. At rest it equals the page's top color
      // so it is invisible and the pills still read as floating; scrolled, it
      // is a solid bar with a hairline. Real iOS frosts this; we paint it flat.
      '.mar-nav-toolbar-row {',
      '  position: sticky;',
      '  top: 0;',
      '  z-index: 10;',  // above in-flow content; far below sheet (2000)
      '  display: flex;',
      '  align-items: center;',
      '  justify-content: space-between;',
      '  gap: 12px;',
      '  box-sizing: border-box;',
      // Full-bleed background: pull the row out to the nav stack's border-box
      // edges (matching its side padding), then pad the content back in so the
      // pills stay in the column. The margins EXACTLY cancel the padding, so
      // the row never widens the document: no horizontal scrollbar (the trap
      // that had forced the old surface onto position:fixed).
      '  margin-left: calc(-1 * max(32px, env(safe-area-inset-left)));',
      '  margin-right: calc(-1 * max(32px, env(safe-area-inset-right)));',
      '  padding-left: max(32px, env(safe-area-inset-left));',
      '  padding-right: max(32px, env(safe-area-inset-right));',
      // The safe-area top lives as PADDING, not a `top` offset: the row pins at
      // top:0 and its background fills up through the status-bar strip, while
      // this padding keeps the pills clear of the notch.
      '  padding-top: max(24px, env(safe-area-inset-top));',
      '  padding-bottom: 6px;',
      '  margin-bottom: 16px;',
      '  min-height: 36px;',
      // Frosted glass, apple.com-style: a translucent tint of the page's TOP
      // color plus a backdrop blur. At rest it sits over the page's own top
      // color, so it reads as that color (invisible); once content scrolls
      // BENEATH the bar (which it does: the whole point of the sticky solid
      // bar) the blur frosts it. The alpha (0.6) is deliberately below
      // apple's own ~0.8: our page content is low-contrast light-gray, so at
      // 0.8 the glass read as nearly solid: 0.6 lets the shapes show through.
      // A heavier blur keeps it legible at that lower opacity.
      //
      // NO saturate(): apple can afford saturate because its page is flat
      // white: there is no hue for it to lift. OUR background is a faintly
      // cool gradient, and saturate() over that tinted backdrop pushed the
      // bar's chroma ABOVE the body's, so AT REST the status-bar strip read a
      // hair cooler than the page right below it: the visible band. Plain
      // blur + a same-color tint shift no hue, so the bar matches the page top
      // exactly at rest and still frosts content on scroll. (If a browser
      // lacks backdrop-filter, 0.6 alone is thin: acceptable, it is rare and
      // modern iOS/desktop all support it.)
      '  background: rgba(247, 247, 249, 0.6);',
      '  -webkit-backdrop-filter: blur(24px);',
      '  backdrop-filter: blur(24px);',
      '  border-bottom: 0.5px solid transparent;',
      '  transition: border-color 180ms ease-out;',
      '}',
      '.mar-nav-toolbar-row.mar-nav-scrolled { border-bottom-color: rgba(0, 0, 0, 0.10); }',

      // Equal flexible sides keep the inline title truly centered
      // regardless of how the left/right pill clusters differ.
      '.mar-nav-toolbar-row > .mar-nav-side { flex: 1 1 0; }',
      // Buttons-only page (no navigationTitle): a touch more gap to the body.
      '.mar-nav-toolbar-solo { margin-bottom: 24px; }',
      // Title-only page (no buttons): the bar is STILL a solid strip, it has
      // to be, to fill the status bar: with only the inline title fading in
      // on scroll. (It used to collapse to height 0 back when the surface was
      // a separate fixed pseudo; now the row itself is the surface, so it must
      // keep its height and just carry no controls.) The class survives only
      // so wireNavInlineTitle can pick the smaller scroll-trigger margin.
      // Inline title: the small centered label that takes over when
      // the large title scrolls out (.mar-nav-scrolled, toggled by
      // wireNavInlineTitle).
      //
      // BARE TEXT, by design. It has no background of its own: legibility
      // comes from the bar's own solid surface behind it, exactly like
      // "Mailboxes" in iOS Mail. That is what makes the pill mean something:
      // every pill in this bar is a control, and the one label that is not a
      // control wears nothing.
      //
      // The cross-fade below is the iOS handoff: opacity plus a 4px rise,
      // over the same window in which the large title leaves. It must not
      // slide in from the top with the bar: the small title arriving on
      // its own is what reads as native.
      '.mar-nav-inline-title {',
      '  font-size: 14px;',
      '  font-weight: 600;',
      '  color: #1d1d1f;',
      '  padding: 8px 0;',
      '  max-width: 50%;',
      '  white-space: nowrap;',
      '  overflow: hidden;',
      '  text-overflow: ellipsis;',
      '  opacity: 0;',
      '  transform: translateY(-4px);',
      '  transition: opacity 200ms ease, transform 200ms ease;',
      '  pointer-events: none;',
      '}',
      '.mar-nav-scrolled .mar-nav-inline-title { opacity: 1; transform: none; }',
      '.mar-nav-title-row { display: block; }',
      '.mar-nav-title {',
      '  font-size: 32px;',
      '  font-weight: 700;',
      '  letter-spacing: -0.02em;',
      '  line-height: 1.15;',
      '  white-space: nowrap;',
      '  overflow: hidden;',
      '  text-overflow: ellipsis;',
      '}',
      '.mar-nav-side { display: flex; align-items: center; gap: 8px; }',
      '.mar-nav-side-trailing { justify-content: flex-end; }',

      // Trailing nav buttons: glass pills. iOS 26 dropped the
      // gray-fill toolbar buttons in favor of translucent pills
      // that pick up backdrop blur. The inset white highlight is
      // the "specular": the cue your eye reads as glass rather
      // than just translucent.
      // Links in nav slots (e.g. an external "Download" in
      // topBarTrailing) wear the same glass pill as buttons: a bare
      // text link floating over scrolling content reads as a glitch
      // next to the title's glass pill.
      '.mar-nav-side .mar-paragraph { margin: 0; }',
      '.mar-nav-side a.mar-inline {',
      '  text-decoration: none;',
      '  display: inline-block;',
      '}',
      '.mar-nav-side button, .mar-nav-side a.mar-inline {',
      // Solid, not glass. The surface behind these is opaque now, so a
      // backdrop blur has nothing left to sample and a translucent fill
      // only reads as washed out. The pill still carries the whole
      // affordance: control wears a container, label never does.
      '  background: #ffffff;',
      '  border: 0.5px solid rgba(0, 0, 0, 0.10);',
      '  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);',
      // Label color, not action color. The affordance here is the PILL
      // itself: controls wear a container, the inline title does not (it
      // is plain text on the bar's surface). Same rule iOS uses:
      // see the Mail app, where "Edit" is a pill and "Mailboxes" is
      // bare text. Tinting these blue was tried and reverted: it made
      // the back button and every trailing action shout for attention
      // in a bar that should be quiet.
      '  color: #1d1d1f;',
      '  font-family: inherit;',
      '  font-size: 14px;',
      '  font-weight: 500;',
      '  padding: 8px 18px;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms, transform 150ms;',
      // touch-action: manipulation tells iOS Safari "this button
      // accepts taps for click: don\'t hold the synthetic click
      // for ~300ms waiting for a possible double-tap-to-zoom".
      // Without it, after a touch-heavy gesture (like the drag
      // reorder), the next tap on this button gets absorbed by
      // Safari\'s gesture-disambiguation window: the operator
      // sees "Done did nothing" on the first press, has to tap
      // again. Applies cleanly to nav-bar action buttons (back,
      // Done, Sign out) which are tap-only: no pinch or scroll.
      '  touch-action: manipulation;',
      // A nav button is a UI.button like any other, so it carries
      // `mar-button` and picks up that rule's spacing. The bar does its
      // own spacing with `gap`, so cancel the margin here rather than
      // letting a stray 8px push the pills off the baseline.
      '  margin: 0;',
      '}',
      '@media (hover: hover) { .mar-nav-side button:hover, .mar-nav-side a.mar-inline:hover { background: #f2f2f2; } }',
      // Disabled in the bar stays a quiet pill: the base rule would paint
      // it the filled gray of a disabled CTA, which is a different object.
      '.mar-nav-side .mar-button:disabled {',
      '  background: #ffffff;',
      '  color: rgba(0, 0, 0, 0.28);',
      '  box-shadow: none;',
      '  opacity: 1;',
      '}',
      '.mar-nav-side button:active, .mar-nav-side a.mar-inline:active { transform: scale(0.96); }',

      // Auto-inserted back button: circular glass pill with the
      // chevron only. iOS 26 dropped the "‹ Back" / "‹ Previous"
      // text label in favor of an icon-only pill that floats over
      // the page. Accessibility lives in `aria-label` and the
      // hover `title` tooltip (set in buildBackButton).
      '.mar-nav-back {',
      '  width: 36px; height: 36px;',
      '  padding: 0;',
      '  border-radius: 50%;',
      '  background: #ffffff;',
      '  border: 0.5px solid rgba(0, 0, 0, 0.10);',
      '  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.05);',
      // Same label color as the trailing actions. The chevron does not
      // need a tint to say "tappable": the round glass pill already
      // does, and a bar where every control shouts in blue is a loud
      // bar. Quiet chrome, loud content.
      '  color: #1d1d1f;',
      '  cursor: pointer;',
      '  display: inline-flex;',
      '  align-items: center;',
      '  justify-content: center;',
      '  transition: background 200ms, transform 150ms;',
      '  touch-action: manipulation;',  // skip iOS 300ms double-tap delay
      '}',
      '@media (hover: hover) { .mar-nav-back:hover { background: #f2f2f2; } }',
      '.mar-nav-back:active { transform: scale(0.92); }',
      '.mar-nav-back-chev {',
      '  font-size: 22px; font-weight: 600; line-height: 1;',
      '  margin-top: -2px;',
      '}',

      // Form / list / nav body: vertical stack with snug gap.
      // Cards have visible boundaries; just enough gap to keep them
      // from touching, no more.
      '.mar-form, .mar-list, .mar-nav-body {',
      '  display: flex; flex-direction: column;',
      '  gap: 24px;',
      '  padding: 0; margin: 0;',
      '  list-style: none;',
      '  border: none;',
      '}',

      // Height propagation. #mar-root is a min-100dvh flex column
      // (see the shell CSS in server.go); the nav stack and its body
      // pass that height down so a `height fill` child or a
      // `centered` view has real space to claim: no magic
      // min-heights anywhere in the chain. Content taller than the
      // viewport still grows normally (flex-basis auto).
      '.mar-nav-stack { flex: 1 1 auto; display: flex; flex-direction: column; }',
      '.mar-nav-body { flex: 1 1 auto; }',

      // Section: small uppercase eyebrow header above a rounded
      // content card. Reset UA margin: some browsers give <section>
      // ~1em vertical margin by default, which shows up as a chunky
      // black gap between consecutive section cards on top of our
      // form gap.
      '.mar-section { display: block; margin: 0; padding: 0; }',
      '.mar-section-header {',
      '  font-size: 12px; font-weight: 600;',
      '  text-transform: uppercase; letter-spacing: 0.8px;',
      '  color: #6e6e73;',
      '  padding: 0 4px; margin: 0 0 4px 0;',
      '}',
      // Section card: iOS 26 "Liquid Glass" surface. Translucent
      // white with backdrop blur + saturation so what\'s behind
      // (the gradient page background) bleeds through softened.
      // The inset specular highlight on the top edge + outer drop
      // shadow give the "floating glass pane above the page" feel.
      //
      // The horizontal inset (16px) lives HERE on the parent rather
      // than as margin on individual rows. That way replaced
      // children (<input>, <textarea>, <select>, naturally fill
      // the (now-narrower) content area via plain `width: 100%`,
      // sidestepping the "width: auto means intrinsic on inputs"
      // CSS gotcha that bit us when the inset was per-row.
      '.mar-section-body {',
      '  background: rgba(255, 255, 255, 0.72);',
      '  -webkit-backdrop-filter: blur(40px) saturate(180%);',
      '  backdrop-filter: blur(40px) saturate(180%);',
      '  border-radius: 18px;',
      '  border: 0.5px solid rgba(0, 0, 0, 0.04);',
      '  overflow: hidden;',
      '  padding: 0 16px;',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.6),',
      '    0 8px 24px rgba(0, 0, 0, 0.05);',
      '}',
      // Rows: vertical padding only, horizontal inset now comes
      // from the parent. Border-bottom becomes "inset divider"
      // (matches iOS Settings rather than full-bleed dividers).
      //
      // `min-height: 22px` matches the height of the edit-mode
      // affordances (drag handle, delete circle). Without this,
      // tapping Edit would grow each row by 2-5px (depending on
      // the browser's default line-height for 16px text) because
      // the chrome is slightly taller than the natural text line.
      // Locking the content area to 22px in normal mode too means
      // the row dimensions are identical in both modes: no
      // jumpy reflow when the user toggles Edit.
      '.mar-section-body > * {',
      '  display: block;',
      '  padding: 10px 0;',
      '  min-height: 22px;',
      '  border-bottom: 0.5px solid rgba(0, 0, 0, 0.08);',
      '  font-size: 16px;',
      '}',
      '.mar-section-body > *:last-child { border-bottom: none; }',
      // Mobile browsers paint their OWN press highlight on a tapped link or
      // button, and they paint it over the element's border box. Every
      // interactive surface here already paints its own press feedback, and
      // the two disagree about the shape: a navigation link's tint bleeds
      // -16px sideways to cover the card's padding and read as a full row
      // (see `a.mar-navigation-link::before`), while the browser's paints
      // only the link box and leaves an untinted strip down each side of the
      // card. On iOS that mismatched rectangle is what shows on tap and
      // stays on screen through the whole interactive back-swipe.
      //
      // So the OS highlight is turned off exactly where this stylesheet has
      // a press state of its own. Anything not on this list (an inline link
      // inside a paragraph, say) keeps the browser's highlight, because
      // there it is the only feedback a touch gets.
      'a.mar-navigation-link,',
      '.mar-button,',
      '.mar-picker, .mar-picker-select,',
      '.mar-nav-back,',
      '.mar-nav-side button, .mar-nav-side a.mar-inline {',
      '  -webkit-tap-highlight-color: transparent;',
      '}',
      // A section with an empty body (e.g. `section [ footer "..." ] []`,
      // a footer- or header-only section) must not paint an empty glass
      // card between its neighbors. Collapse the body so only the
      // header/footer caption shows: matching iOS, where a footer-only
      // section renders as a lone gray caption with no row card.
      '.mar-section-body:empty { display: none; }',
      '.mar-section-footer {',
      '  font-size: 13px; color: #6e6e73;',
      '  padding: 0 4px; margin: 6px 0 0 0;',
      '}',

      // ----- Reorderable section (UI.onMove) -----
      //
      // The section body gets a `mar-section-editing` class when
      // edit mode is on; descendant rules hang off that so a
      // non-edit section pays zero extra cost. Position: relative
      // so the absolute-positioned drop indicator anchors here.
      '.mar-section-editing { position: relative; }',
      // Each row gets a trailing drag handle in edit mode. The
      // row's vertical padding stays (inherited from the
      // .mar-section-body > * rule); we just relayout the content
      // as a flex row so the handle sits at the trailing edge.
      '.mar-section-editing > * {',
      '  display: flex; align-items: center;',
      '  gap: 8px;',
      '}',
      // The handle: 36×22, wide enough for a comfortable
      // horizontal tap target (the row's vertical padding extends
      // the hit area to the full 44pt Apple guideline), and
      // height-matched exactly to the delete button + the row's
      // min-height. Toggling edit mode doesn't cause any reflow
      // because every chrome element (text, delete, handle) is
      // ≤22px tall, and the row's min-height locks the inner
      // area at 22px in both modes.
      //
      // The three bars inside are 18×2 each with a 3px gap,
      // packing to 12px total: comfortably inside the 22px box.
      '.mar-drag-handle {',
      '  flex: 0 0 auto;',
      '  width: 36px; height: 22px;',
      '  display: flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  gap: 3px;',
      '  padding: 0;',
      // `margin-left: auto` makes the handle self-position to the
      // right edge of its flex row, consuming whatever slack the
      // row has. Works whether the row is a plain `text` leaf
      // (where the text node has no flex-grow) or an `hstack`
      // (where the hstack children already fill space). The handle
      // BELONGS at the trailing edge: putting the rule here makes
      // that property of the handle itself, not a side-effect of
      // whatever wrapper happens to be around it.
      '  margin-left: auto;',
      '  background: transparent;',
      '  border: none;',
      '  border-radius: 8px;',
      '  cursor: grab;',
      '  touch-action: none;',  // prevent the browser from scrolling on touch-drag
      '  -webkit-tap-highlight-color: transparent;',
      // Long-press on the handle on iOS shouldn\'t pop the
      // text-selection callout (copy/paste/Define): the handle is
      // a grab affordance, not selectable content.
      '  -webkit-user-select: none; user-select: none;',
      '  -webkit-touch-callout: none;',
      '}',
      '.mar-drag-handle:active { cursor: grabbing; }',
      // The row that hosts this button is rendered as `.mar-hstack`
      // in edit mode (Mar wraps `keyed` rows as hstacks to attach
      // the reorder key). The generic `.mar-hstack > button`
      // chrome below already excludes .mar-drag-handle via
      // :not(), but the failure mode if that exclusion ever
      // misses (cache, chained-:not() bug, future selector
      // refactor) is severe: the handle turns into a solid
      // white pill on hover, making the bars invisible against
      // it. Belt-and-braces: force transparent at higher
      // specificity than the hstack rule, with !important on
      // hover/focus/active.
      '.mar-hstack > .mar-drag-handle,',
      '.mar-hstack > .mar-drag-handle:hover,',
      '.mar-hstack > .mar-drag-handle:focus,',
      '.mar-hstack > .mar-drag-handle:active {',
      '  background: transparent !important;',
      '  border: none !important;',
      '  box-shadow: none !important;',
      '}',
      '.mar-drag-handle-bar {',
      '  display: block;',
      '  width: 18px; height: 2px;',
      '  background: #c7c7cc;',
      '  border-radius: 1px;',
      '  pointer-events: none;',  // so the row's pointerdown fires on the button, not the bar
      '}',
      // Realtime reorder transition. When a drag is in progress the
      // section gets `.mar-section-drag-active`, and non-dragged
      // rows get an inline transform: translateY(±rowHeight) in
      // onPointerMove to "open up" the drop slot under the cursor.
      // This rule applies a CSS transition to those transforms so
      // the shifts animate smoothly instead of snapping per
      // cursor-tick. `:not(.mar-row-dragging)` excludes the dragged
      // row itself, whose follow-cursor transform must update 1:1
      // with the pointer (any transition there would lag the
      // pointer behind the finger).
      '.mar-section-drag-active > *:not(.mar-row-dragging) {',
      '  transition: transform 200ms cubic-bezier(0.32, 0.72, 0, 1);',
      '}',

      // Row being actively dragged: opaque background + elevated
      // shadow + above-siblings z-index so it reads as a card
      // floating over the rest of the list. The pointer-move
      // handler also applies an inline `transform: translateY(...)
      // scale(1.02)` so the row follows the cursor and feels
      // "picked up". The 10px border-radius rounds the floating
      // card slightly so it doesn't look like the same flush row
      // it was at rest.
      //
      // Border-bottom suppressed: the `.mar-section-body > *` rule
      // gives every child a hairline divider, which on a card
      // that's now floating reads as a stray underline.
      '.mar-row-dragging {',
      '  background: #ffffff;',
      '  box-shadow: 0 12px 28px rgba(0, 0, 0, 0.22), 0 4px 8px rgba(0, 0, 0, 0.08);',
      '  border-radius: 10px;',
      '  border-bottom: none !important;',
      '  z-index: 2;',
      '  position: relative;',
      '}',
      // Keyboard focus on a row: Tab navigates between rows in
      // edit mode (each has tabindex="0"). Without an explicit
      // :focus-visible rule, the browser draws its default outline
      // which gets visually swallowed by the row's flush layout
      // and the dark page background. A subtle blue outline makes
      // "I just Tabbed here" obvious: same color as the grabbed
      // state below, but no background tint so the two states
      // remain distinguishable (focused = "could grab", grabbed =
      // "actively moving").
      '.mar-section-editing > *:focus-visible {',
      '  outline: 2px solid #0a84ff;',
      '  outline-offset: -2px;',
      '  border-radius: 8px;',
      '}',
      // Keyboard "grabbed" state: distinct from focused (adds the
      // background tint to say "arrow keys will move this row").
      // Both styles share the outline so the visual transition
      // from focused → grabbed is just the bg-fill flashing on.
      '.mar-row-grabbed {',
      '  outline: 2px solid #0a84ff;',
      '  outline-offset: -2px;',
      '  background: rgba(10, 132, 255, 0.08);',
      '  border-radius: 8px;',
      '}',
      // Focus, grab, and active-drag states all stretch the
      // highlight to the section card's edges. The section-body
      // has `padding: 0 16px` around its rows; without
      // compensating, the outline / bg tint / dragged-card surface
      // floats inset from the card's edges with a 16px gap that
      // reads as "this is some inner thing, not the whole row".
      //
      // Negative left/right margins (-16px) pull the row's BOX out
      // to the section's padding edges; equal left/right padding
      // (16px) restores the content position so text / buttons /
      // handle don't shift. Result: outline + bg + lift span the
      // full row width (edge to edge of the rounded card),
      // content stays in its place: the iOS Settings "selected
      // row" look.
      //
      // Including `.mar-row-dragging` here makes the lifted card
      // also fill the full width so the dragged surface visually
      // detaches from the section's inner padding rails.
      '.mar-section-editing > *:focus-visible,',
      '.mar-section-editing > *.mar-row-grabbed,',
      '.mar-section-editing > *.mar-row-dragging {',
      '  margin-left: -16px;',
      '  margin-right: -16px;',
      '  padding-left: 16px;',
      '  padding-right: 16px;',
      '}',
      // Corner rounding for the first and last rows: match the
      // section card's outer border-radius (18px) so the outline
      // and bg fill curve smoothly into the card's rounded
      // corners. Without these overrides, the rectangular 8px
      // radius of the row's highlight crosses through the card's
      // 18px curved corner area, leaving a visible "ledge" where
      // the row outline's straight edge cuts across the card's
      // bend. Middle rows keep the default 8px (flat top/bottom
      // edges, since they border other rows).
      //
      // The `:first-child:last-child` combo handles the single-row
      // case: both top AND bottom corners round.
      '.mar-row-first:focus-visible,',
      '.mar-row-first.mar-row-grabbed {',
      '  border-radius: 18px 18px 8px 8px;',
      '}',
      '.mar-row-last:focus-visible,',
      '.mar-row-last.mar-row-grabbed {',
      '  border-radius: 8px 8px 18px 18px;',
      '}',
      '.mar-row-first.mar-row-last:focus-visible,',
      '.mar-row-first.mar-row-last.mar-row-grabbed {',
      '  border-radius: 18px;',
      '}',
      // Drop indicator: thin blue line shown between rows during
      // drag. Absolutely positioned over the list root; the JS
      // updates `top` as the cursor moves.
      //
      // The explicit `padding: 0; border: 0` overrides the
      // `.mar-section-body > *` rule above which gives every child
      // 10px vertical padding + a hairline border-bottom. Without
      // these overrides the "2px line" actually renders as a ~22px
      // blob (2px content + 20px padding + 0.5px border): visible
      // as a fat blue bar in the screenshot. Absolute positioning
      // takes the element OUT OF FLOW but it still inherits the
      // box-model properties applied by the descendant selector.
      '.mar-drop-indicator {',
      '  position: absolute;',
      '  left: 0; right: 0;',
      '  height: 2px;',
      '  padding: 0;',
      '  border: 0;',
      '  background: #0a84ff;',
      '  border-radius: 1px;',
      '  pointer-events: none;',
      '  z-index: 1;',
      '}',
      // ARIA live region: visually hidden but exposed to screen
      // readers. The reorder code writes "Grabbed item 2 of 5"
      // etc. here, the screen reader announces it politely.
      '.mar-live-region {',
      '  position: absolute;',
      '  width: 1px; height: 1px;',
      '  margin: -1px; padding: 0;',
      '  overflow: hidden;',
      '  clip-path: inset(50%);',  // modern replacement for the deprecated clip: rect()
      '  white-space: nowrap; border: 0;',
      '}',
      // Dark mode handle / grabbed adjustments.
      //
      // For the dragged row in dark mode: light-mode goes #ffffff
      // (clearly lighter than the page), so dark mode needs the
      // opposite contrast: a tint LIGHTER than the surrounding
      // section-body card, with a stronger shadow to read as
      // "floating above" against the near-black page. Bumping to
      // rgba(72,72,74,…) (≈ iOS systemGray3) lifts it visibly
      // above the section-body's rgba(44,44,46,…) backing.
      //
      '@media (prefers-color-scheme: dark) {',
      '  .mar-drag-handle-bar { background: rgba(255, 255, 255, 0.4); }',
      '  .mar-row-dragging {',
      '    background: rgb(72, 72, 74);',
      '    box-shadow: 0 12px 28px rgba(0, 0, 0, 0.7), 0 4px 8px rgba(0, 0, 0, 0.4);',
      '  }',
      '  .mar-row-grabbed {',
      '    background: rgba(10, 132, 255, 0.18);',
      '  }',
      '}',

      // Stacks
      '.mar-hstack {',
      '  display: flex; flex-direction: row; align-items: center;',
      '  gap: 12px;',
      '}',
      // SwiftUI-style layout: hstack children HUG their content, they
      // do NOT stretch to fill the row. To distribute, insert a
      // `spacer` (pushes siblings apart) or wrap a child in `expand`
      // (claims the free space). This is the single rule. The only
      // children that stay greedy are the ones that are intrinsically
      // flexible (textField / textArea, mirroring SwiftUI\'s TextField)
      // or ARE the fill mechanism (spacer, expand): each opts back in
      // below. `flex: 0 1 auto` = hug, but shrink (truncate) rather
      // than overflow on a narrow row.
      '.mar-hstack > * { flex: 0 1 auto; min-width: 0; }',
      // Buttons keep their intrinsic size and never compress: a
      // clipped button label reads as broken.
      '.mar-hstack > button { flex: 0 0 auto; }',
      '.mar-vstack {',
      '  display: flex; flex-direction: column; align-items: stretch;',
      // 6px is the SwiftUI-feel default: tight enough that a
      // title/subtitle pair reads as a single row (the iOS Settings
      // pattern), loose enough that a vstack of three sibling
      // sentences still has breathing room. The previous 12px
      // pushed two-line nav rows to feel like two separate items.
      '  gap: 6px;',
      '}',

      // Inputs: soft fill, big, prominent. Focus shows the
      // link-blue ring. Always has visible chrome (border, fill,
      // rounded, focus ring). What changes with context is
      // POSITIONING:
      //   - inside a section card: vertical+horizontal margin so the
      //     input is visually inset from the card edges
      //   - inside an hstack: flex: 1 (no margin, hstack's gap
      //     handles spacing)
      //   - free-floating: full width
      //
      // -webkit-appearance: none kills Safari's UA textfield chrome
      // (white pill background) that otherwise wins over our styles.
      '.mar-textfield {',
      '  display: block; box-sizing: border-box;',
      '  width: 100%;',
      // Explicitly cancel the `max-width: 24rem` set by the HTML\'s
      // baseline `<style>` reset (see internal/jsserve/server.go). That
      // reset uses a `:where(input:not(...))` selector with zero
      // specificity, expecting any rule that mentions `.mar-textfield`
      // by class to win, but specificity comparison happens per
      // PROPERTY, not per rule. `.mar-textfield` declares `width` but
      // not `max-width`, so the reset\'s 24rem cap leaks through and
      // hard-caps every mar-styled input at 384px regardless of
      // container. Setting `max-width: none` here neutralizes it.
      '  max-width: none;',
      '  margin: 0;',
      '  padding: 10px 14px;',
      '  border: 1px solid rgba(0, 0, 0, 0.12);',
      '  border-radius: 8px;',
      '  outline: none;',
      '  -webkit-appearance: none; appearance: none;',
      '  background: rgba(0, 0, 0, 0.02);',
      '  font-size: 16px; font-family: inherit; color: inherit;',
      '  transition: background 200ms, border-color 200ms, box-shadow 200ms;',
      '}',
      '.mar-textfield:focus {',
      '  background: #ffffff;',
      '  border-color: rgba(0, 113, 227, 0.5);',
      '  box-shadow: 0 0 0 4px rgba(0, 113, 227, 0.12);',
      '}',
      '.mar-textfield::placeholder { color: #86868b; }',
      // Tabular numerals: every digit (0-9) renders at exactly the
      // same advance width, making CSS `ch` calculations exact for
      // numeric-content inputs. Without this, in proportional fonts
      // like system-ui / SF Pro, "888888" is ~5% wider than "111111",
      // causing horizontal scroll-flicker when the cursor passes the
      // trailing edge of a sized field (e.g. `width (chars 6)` on a
      // 6-digit code input).
      //
      // Scoped to inputs with `inputmode="numeric"` (set by `numeric` /
      // `numericCode` attrs) so non-numeric fields keep proportional
      // digits, which read better in regular prose.
      '.mar-textfield[inputmode="numeric"] {',
      '  font-variant-numeric: tabular-nums;',
      '}',
      // Disabled textfield / textarea: keeps the same visual frame
      // but dims the text + placeholder and switches the cursor so
      // the user knows clicking won\'t do anything. `not-allowed`
      // matches the cursor we use on disabled buttons.
      '.mar-textfield:disabled {',
      '  background: rgba(0, 0, 0, 0.02);',
      '  color: rgba(0, 0, 0, 0.36);',
      '  cursor: not-allowed;',
      '  border-color: rgba(0, 0, 0, 0.06);',
      '}',
      '.mar-textfield:disabled::placeholder { color: rgba(0, 0, 0, 0.24); }',
      // Multi-line textarea variant. Inherits all the .mar-textfield
      // base styling (borders, focus ring, padding) and adds the bits
      // that don't apply to <input>: a relaxed line-height, vertical
      // resize affordance, and a font-family pin so it renders in the
      // same sans the rest of the form uses (browsers default
      // <textarea> to a monospace stack on some platforms).
      '.mar-textarea {',
      '  line-height: 1.4;',
      '  font-family: inherit;',
      '  resize: vertical;',
      '  min-height: 80px;',
      '}',
      // Inside a section card: just a small vertical margin between
      // rows. The horizontal inset comes from the parent's padding
      // (see `.mar-section-body`), so the textfield's base
      // `width: 100%` already fills the card, no per-row width math.
      '.mar-section-body > .mar-textfield {',
      '  margin: 8px 0;',
      '}',
      // Inside an hstack, fill the available row, no margin.
      '.mar-hstack > .mar-textfield {',
      '  flex: 1; min-width: 0;',
      '  margin: 0;',
      '}',

      // Picker: single-selection dropdown. The wrapping div is the
      // visual chrome (border, focus state, custom chevron); the
      // inner <select> stays a plain native control so accessibility
      // (keyboard nav, screen-reader, mobile native popover) keeps
      // working. We hide the browser's default chevron via
      // appearance:none so the only chevron the user sees is our
      // overlay glyph, which we can position consistently across
      // platforms.
      // Date picker: a native <input type="date"> wearing the
      // textfield chrome (.mar-textfield). We only nudge the calendar
      // trigger so it reads as interactive; .mar-textfield's
      // appearance:none can otherwise dim it.
      '.mar-datepicker { cursor: pointer; }',
      '.mar-datepicker::-webkit-calendar-picker-indicator {',
      '  cursor: pointer; opacity: 0.55;',
      '}',
      '.mar-datepicker::-webkit-calendar-picker-indicator:hover { opacity: 0.9; }',

      '.mar-picker {',
      '  display: flex; align-items: center;',
      // `isolation: isolate` keeps the ::before's `z-index: -1`
      // inside the picker's local stacking context. Without it,
      // the negative z-index escapes to the html root and paints
      // behind the section card's white background: invisible.
      // Same fix as `a.mar-navigation-link`.
      '  position: relative;',
      '  isolation: isolate;',
      '  cursor: pointer;',
      '  user-select: none;',
      '}',
      '.mar-picker-select {',
      '  flex: 1; min-width: 0;',
      '  appearance: none; -webkit-appearance: none;',
      '  background: transparent;',
      '  border: none;',
      // 24px right pad reserves space for the chevron; the chevron
      // is absolutely positioned at right: 0 (see below) and would
      // otherwise overlap the longest option label.
      '  padding: 4px 24px 4px 0;',
      '  margin: 0;',
      '  font: inherit; color: inherit;',
      // Left-align the value. Matches the readability the user
      // asked for: the eye scans from the section header on the
      // left, and the trailing chevron at the row's right edge
      // closes the visual frame. (SwiftUI Form Picker right-aligns
      // the value flush against the chevron; we don\'t copy that
      // here because long values on web tend to feel disconnected
      // from the section header when right-aligned.)
      '  text-align: left;',
      '  text-align-last: left;',
      '  cursor: pointer;',
      '  outline: none;',
      '}',
      // The chevron sits in the trailing slot of the row, mirroring
      // SwiftUI's Picker (which shows the value flush-right + a
      // menu indicator). aria-hidden in markup; dimmed in CSS so
      // it reads as decorative chrome. Glyph is the two-arrow
      // "select-up-down" indicator: the iOS 17+ Picker(.menu)
      // convention. Apple's symbol is `chevron.up.chevron.down`;
      // we render it inline via a stacked SVG that ships with the
      // runtime so it renders identically across fonts (`›`
      // rotated is jagged on some renderers; standalone `⌄` looks
      // like a typo).
      //
      // Sized to roughly match the visual weight of the
      // navigationLink chevron (1.4em `›`). The SVG itself is
      // 18×22; container box matches so the strokes don\'t get
      // anti-aliased into a blur.
      '.mar-picker-chevron {',
      '  position: absolute; right: 2px;',
      '  top: 50%; transform: translateY(-50%);',
      '  display: inline-flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  width: 18px; height: 22px;',
      '  color: #8e8e93;',
      '  line-height: 1;',
      '  pointer-events: none;',
      '}',
      // Inside a section card, same row layout as toggle: label-ish
      // text on the left (the selected value) and the trailing
      // disclosure chevron on the right. The section-body > * rule
      // supplies the 10px/16px padding so we don\'t restate it here.
      '.mar-section-body > .mar-picker {',
      '  width: auto;',
      '}',
      // Keyboard-focus tint, full-bleed across the row. Same pattern
      // as navigationLink hover (inset: -1px -16px): a ::before
      // pseudo paints the focus background past the picker's own box
      // so the cyan tint reaches the card's inner edge instead of
      // leaving white strips where the parent's 16px padding lives.
      // The card's overflow:hidden + 18px border-radius clips the
      // ::before at the card corners; no mismatched-radii problem.
      // Anchored against the picker's existing `position: relative`.
      '.mar-picker::before {',
      '  content: "";',
      '  position: absolute;',
      '  inset: -1px -16px;',
      '  background: transparent;',
      '  pointer-events: none;',
      '  transition: background 120ms;',
      '  z-index: -1;',
      '}',
      // Hover tint matches navigationLink (a sibling row in the
      // same section card) so the two row shapes feel like one
      // affordance vocabulary. Source order matters: hover first,
      // focus-within second, so a focused-AND-hovered picker
      // shows the focus blue, not the hover gray. Same precedence
      // as navigationLink.
      //
      // `:not(.mar-disabled)` suppresses BOTH tints when the picker
      // is inert: matches the convention used by button/
      // navigationLink/etc: disabled = ZERO interactive feedback,
      // only the `not-allowed` cursor signals "this exists but
      // doesn\'t respond." Without this guard, a disabled picker
      // would light up gray on hover, falsely inviting interaction.
      // Same sticky-hover-on-touch guard as navigationLink: `:active` carries
      // the press tint everywhere (transient), `:hover` is gated to pointers.
      '.mar-picker:not(.mar-disabled):active::before {',
      '  background: rgba(0, 0, 0, 0.04);',
      '}',
      '@media (hover: hover) {',
      '  .mar-picker:not(.mar-disabled):hover::before {',
      '    background: rgba(0, 0, 0, 0.04);',
      '  }',
      '}',
      // Keyboard-only. A native <select> keeps focus after a mouse
      // click, so :focus-within left this tint stuck until a second
      // click elsewhere finally blurred it. :has(:focus-visible) shows
      // the indicator for keyboard (Tab) focus and drops it for mouse,
      // which is what "keyboard-focus tint" meant all along.
      '.mar-picker:not(.mar-disabled):has(.mar-picker-select:focus-visible)::before {',
      '  background: rgba(0, 113, 227, 0.10);',
      '}',
      // Disabled picker: fade the value text + chevron and switch
      // the cursor. The inner <select disabled> blocks the dropdown;
      // .mar-disabled is toggled on the wrapper by the renderer so
      // the chevron (a separate <span>) and any future overlay
      // chrome can react too.
      '.mar-picker.mar-disabled { cursor: not-allowed; }',
      '.mar-picker.mar-disabled .mar-picker-select {',
      '  color: rgba(0, 0, 0, 0.36);',
      '  cursor: not-allowed;',
      '}',
      '.mar-picker.mar-disabled .mar-picker-chevron {',
      '  color: rgba(0, 0, 0, 0.18);',
      '}',

      // Buttons split into two roles (iOS 26 hierarchy):
      //
      //   PRIMARY CTA  - section-body direct child. Full-width
      //                  action like "Verify" / "Submit". Solid
      //                  blue fill, white text: the visual
      //                  weight reads "do this".
      //
      //   SECONDARY    - hstack child (inline next to other row
      //                  content, like "Add" beside an input, or
      //                  "Delete" in a task row). Glass pill with
      //                  link-blue TEXT instead of solid blue:
      //                  quiet-action treatment.
      '.mar-button {',
      '  display: block;',
      '  width: auto;',
      '  margin: 8px 16px;',
      '  padding: 10px 20px;',
      '  background: #0071e3; border: none; color: white;',
      '  font-family: inherit; font-weight: 500; font-size: 15px;',
      '  text-align: center;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms, opacity 200ms, transform 120ms;',
      '  touch-action: manipulation;',  // skip iOS 300ms double-tap delay
      '}',
      // Hover tint is gated to real pointers: on iOS Safari an ungated
      // :hover STICKS to the tapped button (hover emulation), leaving it
      // looking selected while the tap sometimes fails to register: you
      // tap once, see the highlight, and nothing happens. Touch gets a
      // transient :active press instead, which clears on release.
      '@media (hover: hover) {',
      '  .mar-button:hover { background: #0077ed; }',
      '  .mar-button:disabled:hover { background: #c7c7cc; }',
      '}',
      '.mar-button:active:not(:disabled) { transform: scale(0.97); }',
      '.mar-button:disabled {',
      '  background: #c7c7cc; color: rgba(255, 255, 255, 0.85);',
      '  cursor: not-allowed; opacity: 0.55;',
      '}',
      // Generic `.mar-hstack > button` chrome: the pill / blur /
      // blue-link look for action buttons inline in a row (Add, Save,
      // etc.). Two buttons are EXCEPTIONS and need their own visual
      // identity preserved, so they're excluded via :not():
      //   - .mar-drag-handle: the three-bars reorder grip. The hstack
      //     style would give it the same pill look as Add and turn it
      //     into yet another generic button; we want the bare gray
      //     bars.
      //   - .mar-row-delete: the red minus-circle delete affordance.
      //     The hstack rule's background + border-radius:980px + blue
      //     color would turn it into a pill instead of the red round
      //     destructive-action button defined down in the
      //     onDelete-affordance CSS.
      // Both exclusions matter because the row is rendered as an
      // hstack in edit mode (see Frontend/Home.mar's renderTaskRow);
      // the delete button + drag handle get appended INTO that
      // hstack, where they would otherwise inherit these styles via
      // selector specificity ((0,1,1) > (0,1,0)).
      '.mar-hstack > .mar-button {',
      '  background: rgba(255, 255, 255, 0.62);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.06);',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 2px 8px rgba(0, 0, 0, 0.04);',
      '  color: #0071e3;',
      '  font-family: inherit; font-weight: 500;',
      '  border-radius: 980px;',
      '  cursor: pointer;',
      '  transition: background 200ms, transform 150ms;',
      // touch-action: manipulation skips iOS\'s 300ms double-tap delay.
      // The :not() exclusions matter because the drag handle needs
      // touch-action: none for pointer-drag, and the delete button
      // has its own rule below.
      '  touch-action: manipulation;',
      '}',
      // Same iOS sticky-hover gate as the section-body button above; the
      // :active press stays (it already gave touch its feedback).
      '@media (hover: hover) {',
      '  .mar-hstack > .mar-button:hover { background: rgba(255, 255, 255, 0.88); }',
      '  .mar-hstack > .mar-button:disabled:hover { background: rgba(0, 0, 0, 0.04); }',
      '}',
      '.mar-hstack > .mar-button:active { transform: scale(0.96); }',
      '.mar-hstack > .mar-button:disabled {',
      '  background: rgba(0, 0, 0, 0.04);',
      '  color: rgba(0, 0, 0, 0.28);',
      '  cursor: not-allowed;',
      '  box-shadow: none;',
      '}',
      // Inline pill (next to an input).
      '.mar-hstack > .mar-button {',
      '  flex: 0 0 auto;',
      '  margin: 0;',
      '  padding: 6px 16px;',
      '  font-size: 14px;',
      '}',

      // hstack inside section card: already gets row padding from
      // the section-body > * rule. No extra needed.

      // Title / subtitle: heading text in the body of a section.
      // Scoped via class to avoid clobbering h1/h2 used elsewhere
      // (e.g. .mar-section-header is also <h2>).
      '.mar-title {',
      '  font-size: 24px; font-weight: 700; letter-spacing: -0.015em;',
      '  margin: 0; line-height: 1.2;',
      '}',
      '.mar-subtitle {',
      '  font-size: 17px; font-weight: 400; color: #6e6e73;',
      '  margin: 0; line-height: 1.35;',
      '}',
      // image, UI.image. Default (no `size` attr): fills the
      // container width and keeps aspect ratio. A `size` attr sets
      // explicit width/height inline (see applyImageAttrs). Rounded
      // corners match the section-card radius language; object-fit
      // defaults to contain (no crop) and flips to cover under `fill`.
      // canvas draw-surface (2D games). Fills the viewport: the only
      // consumer today is a full-screen game whose `canvas` is the page's
      // root view. touch-action/user-select are off so taps don't pan,
      // zoom, or select. The element reports its box back via watchSize.
      '.mar-canvas {',
      '  display: block;',
      '  width: 100%;',
      '  height: 100dvh;',
      '  touch-action: none;',
      '  user-select: none;',
      '  -webkit-user-select: none;',
      // iOS Safari otherwise pops the copy/paste callout (and can start a
      // selection) on a long-press over the game surface: kill it.
      '  -webkit-touch-callout: none;',
      '}',
      // Page-wide armor for game pages (root view IS a canvas; the
      // render loop tags <html> with .mar-canvas-page: see
      // syncCanvasRootClass). The per-element rules above are not
      // enough on iOS Safari: a long-press over a non-selectable
      // element can still start a selection on the nearest selectable
      // ancestor (html/body), and the dynamic-viewport dance around
      // the URL bar can land a touch a hair outside the canvas. Games
      // long-press constantly (hold-to-jump), so disarm the page.
      // Regular UI pages never get the class: text stays selectable.
      'html.mar-canvas-page, html.mar-canvas-page body {',
      '  user-select: none;',
      '  -webkit-user-select: none;',
      '  -webkit-touch-callout: none;',
      '  touch-action: none;',
      '  overscroll-behavior: none;',
      '}',
      '.mar-image {',
      '  display: block; max-width: 100%; height: auto;',
      '  border-radius: 10px; object-fit: contain;',
      '}',
      // errorText: red + semi-bold so "couldn't reach the server"
      // and similar destructive-state messages jump out from the
      // surrounding plain body text. The shade is iOS systemRed
      // (#ff3b30) which has good contrast on both light and dark
      // section-card backgrounds; the dark-mode override below
      // shifts to a slightly lighter variant for legibility against
      // the darker card.
      '.mar-error-text {',
      '  font-size: 15px; font-weight: 600;',
      '  color: #ff3b30;',
      '  margin: 0; line-height: 1.4;',
      '}',
      '@media (prefers-color-scheme: dark) {',
      '  .mar-error-text { color: #ff6961; }',
      '}',
      // "A deploy happened while this tab was open." Bottom-left rather
      // than full width: the news is real but small, and a bar across
      // the screen would read as an error. One z-index below the runtime
      // failure banner, which always outranks it, if the app is broken,
      // "there is a newer version" is not the headline.
      '#mar-stale-deploy {',
      '  position: fixed; z-index: 2147482999;',
      '  left: 12px;',
      '  bottom: calc(12px + env(safe-area-inset-bottom, 0px));',
      '  display: flex; align-items: center; gap: 10px;',
      '  padding: 8px 10px 8px 14px;',
      '  border-radius: 999px;',
      '  background: #1c1c1e; color: #f2f2f7;',
      '  font-size: 13px; line-height: 1;',
      '  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.22);',
      '  max-width: calc(100vw - 24px);',
      '}',
      '#mar-stale-deploy button {',
      '  font: inherit; font-weight: 600;',
      '  color: #1c1c1e; background: #f2f2f7;',
      '  border: 0; border-radius: 999px;',
      '  padding: 6px 12px; cursor: pointer;',
      '}',
      // Hover only where hovering exists: on iOS Safari a :hover rule
      // sticks to the element after a tap and the button stays lit.
      '@media (hover: hover) {',
      '  #mar-stale-deploy button:hover { background: #ffffff; }',
      '}',
      // Runtime failures (ADR 0020). Deliberately plain: no animation, no
      // entrance, nothing that moves. The message is already the worst news
      // the app can give, and a bug that fires every frame would turn any
      // motion here into a strobe.
      '.mar-runtime-error {',
      '  background: #fff2f1; border: 1px solid #ffd0cc;',
      '  border-radius: 12px; padding: 16px 18px;',
      '  color: #7a1c14; text-align: left;',
      '}',
      // The banner: pinned to the bottom because the screen behind it is
      // still the live app and still usable.
      '.mar-runtime-error-dispatch {',
      '  position: fixed; z-index: 2147483000;',
      '  left: 12px; right: 12px;',
      '  bottom: calc(12px + env(safe-area-inset-bottom, 0px));',
      '  box-shadow: 0 2px 12px rgba(0, 0, 0, 0.14);',
      '}',
      // The page cases replace the screen, so they sit in the content flow.
      '.mar-runtime-error-page, .mar-runtime-error-stuck {',
      '  margin: 24px 16px;',
      '}',
      '.mar-runtime-error-title {',
      '  font-size: 17px; font-weight: 600; margin: 0 0 8px;',
      '}',
      '.mar-runtime-error-line {',
      '  font-size: 15px; line-height: 1.45; margin: 0 0 4px;',
      '}',
      '.mar-runtime-error-detail {',
      '  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;',
      '  font-size: 13px; line-height: 1.5; margin: 0;',
      '  white-space: pre-wrap; overflow-wrap: anywhere;',
      '}',
      '.mar-runtime-error-back {',
      '  margin-top: 12px; padding: 8px 16px;',
      '  font: inherit; font-size: 15px;',
      '  color: #7a1c14; background: transparent;',
      '  border: 1px solid #e0a49d; border-radius: 8px;',
      '  cursor: pointer;',
      '}',
      '@media (prefers-color-scheme: dark) {',
      '  .mar-runtime-error {',
      '    background: #2a1512; border-color: #5c2a24; color: #ffb3aa;',
      '  }',
      '  .mar-runtime-error-back { color: #ffb3aa; border-color: #7a3a32; }',
      '}',
      // paragraph + inline atoms: flowing block of mixed-styled
      // text. The block sets natural body-text dimensions; inline
      // atoms compose freely via additive CSS classes so `[bold,
      // code]` (bold inline code) works without special-casing.
      '.mar-paragraph {',
      '  font-size: 17px; line-height: 1.4;',
      '  margin: 0;',
      '  color: inherit;',
      // overflow-wrap so long URLs in `link` text break instead of
      // forcing horizontal scroll on narrow screens.
      '  overflow-wrap: anywhere;',
      '}',
      '.mar-inline { color: inherit; }',
      '.mar-inline-bold { font-weight: 700; }',
      '.mar-inline-italic { font-style: italic; }',
      '.mar-inline-strike { text-decoration: line-through; }',
      // Inline code: monospace + subtle background tint + small
      // radius. Padding kept tight (1px x 4px) so the run sits on
      // the same baseline as surrounding text without bumping the
      // line-height.
      '.mar-inline-code {',
      '  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;',
      '  font-size: 0.92em;',
      '  background: rgba(0, 0, 0, 0.06);',
      '  padding: 1px 4px; border-radius: 4px;',
      '}',
      // Consecutive inline-code spans merge into one continuous pill: drop the
      // inner padding + rounding so a run like `[code,bold]"List.map"` followed
      // by `[code]" : a -> b"` reads as a single code fragment (bold name and
      // all), with no seam between the two backgrounds.
      '.mar-inline-code:has(+ .mar-inline-code) {',
      '  padding-right: 0; border-top-right-radius: 0; border-bottom-right-radius: 0;',
      '}',
      '.mar-inline-code + .mar-inline-code {',
      '  padding-left: 0; border-top-left-radius: 0; border-bottom-left-radius: 0;',
      '}',
      '@media (prefers-color-scheme: dark) {',
      '  .mar-inline-code { background: rgba(255, 255, 255, 0.10); }',
      '}',
      // Inline link: accent color + underline. Underline is the
      // affordance signal; on hover the underline thickens slightly
      // for feedback without changing position (text-decoration-
      // thickness keeps layout stable).
      '.mar-inline-link {',
      '  color: #0a84ff; text-decoration: underline;',
      '  text-decoration-thickness: 1px;',
      '  text-underline-offset: 2px;',
      '}',
      '.mar-inline-link:hover { text-decoration-thickness: 2px; }',
      '.mar-inline-link:focus-visible {',
      '  outline: 2px solid #0a84ff; outline-offset: 2px;',
      '  border-radius: 2px;',
      '}',
      // navigationLink: full-width tappable area with a chevron
      // on the trailing edge, mirroring how iOS NavigationLink
      // renders inside a list. Child content keeps the inherited
      // text color so a vstack of text+subtitle (typical row label)
      // reads naturally without restyling.
      'a.mar-navigation-link {',
      '  display: flex; align-items: center; justify-content: space-between;',
      '  gap: 12px;',
      '  width: 100%;',
      // Reserve a right-side gutter for the "›" chevron (which is drawn
      // absolutely at right:0). Without it, a spacer-pushed trailing value
      // (e.g. "SUA VEZ" flush-right) lands on top of the chevron. The
      // chevron stays visually pinned to the far right (it's absolute, so
      // padding doesn't move it); this only insets the CONTENT so it stops
      // just before the chevron: the iOS Settings layout. Left-only rows
      // are unaffected (nothing sits in the gutter to see a difference).
      '  padding-right: 20px;',
      '  color: inherit; text-decoration: none; cursor: pointer;',
      // No explicit padding: the parent `.mar-section-body > *`
      // rule supplies 10px 0 to every row inside a section card,
      // and the parent itself supplies the 16px horizontal inset
      // via its own `padding: 0 16px`. The link content sits
      // visually inset from the card edges that way.
      //
      // `position: relative` anchors the ::before that paints the
      // hover/focus tint full-bleed (see below).
      //
      // `isolation: isolate` forces a stacking context on the link.
      // Without it, the ::before's `z-index: -1` escapes to the
      // nearest stacking-context ancestor (the html root) and ends
      // up painting BEHIND the section card's white background:
      // so hover + focus tints become invisible. With isolation,
      // the ::before stays inside the link's local context: behind
      // the link's static content (text + chevron), above the
      // section card's background.
      '  position: relative;',
      '  isolation: isolate;',
      '}',
      'a.mar-navigation-link::after {',
      '  content: "›";',
      '  color: #c7c7cc; font-size: 1.4em; line-height: 1;',
      // The chevron is a decorative trailing ACCESSORY, so it must not
      // shrink the link's content box. As a flex sibling (the old
      // `flex-shrink: 0`) it consumed its own width + the 12px gap, so an
      // `expand`-ed two-column child inside the link laid its columns out
      // over (rowWidth - chevron - gap): shifting a right-hand value
      // column ~10px left of the same column in a plain (chevron-less)
      // row. Taking it out of flow (absolute) gives the content the FULL
      // row width, so a value/detail column lines up identically whether
      // or not the row has a chevron. It still sits at the right edge,
      // vertically centred, painted over the (empty) trailing margin.
      '  position: absolute; right: 0; top: 50%; transform: translateY(-50%);',
      '}',
      // Hover background: paints full-bleed across the row including
      // the card's 16px horizontal padding. A naive
      // `:hover { background }` on the link only covers the link's
      // own box, leaving white strips where the parent padding sits
      // (the "weird border" bug).
      //
      // Geometry:
      //   - left/right -16px: extend past the parent card's
      //     horizontal padding so the tint reaches the card's inner
      //     border. `.mar-section-body` has overflow:hidden so any
      //     bleed past the card's content box is clipped.
      //   - top/bottom -1px: bleeds past the link's padding box just
      //     enough to cover the 0.5px row divider (this row's own
      //     border-bottom, and the previous row's border-bottom that
      //     sits flush against this row's top). Without this, the
      //     hover tint leaves a thin white sliver at the row's top
      //     and bottom edges: visible at standard zoom.
      //
      // Anchored against the link's `position: relative`. z-index: -1
      // keeps the paint within the link's stacking context (so it
      // doesn't bleed into siblings) while still appearing BELOW
      // the link's static content (text + chevron).
      'a.mar-navigation-link::before {',
      '  content: "";',
      '  position: absolute;',
      '  inset: -1px -16px;',
      '  background: transparent;',
      '  pointer-events: none;',
      '  transition: background 120ms;',
      '  z-index: -1;',
      '}',
      // Mobile browsers emulate `:hover` on tap and leave it STUCK on the
      // last-tapped element (sticky-hover / bfcache restore), so the tint
      // reappears for a moment when you navigate back to a list. Gate the
      // hover tint to real-pointer devices; `:active` gives the same press
      // feedback on touch but is transient (clears on touchend / navigation),
      // so nothing sticks on return.
      'a.mar-navigation-link:active::before { background: rgba(0,0,0,0.07); }',
      '@media (hover: hover) {',
      '  a.mar-navigation-link:hover::before { background: rgba(0,0,0,0.07); }',
      '}',
      // Keyboard-focus tint. Without this, Tab navigation lands on
      // the link with no visible change: users assume Tab is
      // skipping the row entirely. Same ::before painter as hover,
      // brighter color so focus reads distinctly from a casual
      // mouseover. `:focus-visible` (not `:focus`) so a click on
      // the row doesn't leave a ring behind after the page changes.
      // The link's own outline is suppressed since the tint plays
      // the role of focus indicator full-bleed.
      'a.mar-navigation-link:focus { outline: none; }',
      'a.mar-navigation-link:focus-visible::before { background: rgba(0, 113, 227, 0.10); }',
      // Disabled navigationLink: fade the text + chevron, kill the
      // hover background, cursor flips to `not-allowed`. The click
      // itself is swallowed by the delegated handler (onLinkClick
      // reads __marView.attrs and bails out for disabled links):
      // so we DON\'T set `pointer-events: none` here: that would
      // also suppress the cursor change, leaving the link looking
      // identical to its enabled sibling on hover. Trade-off:
      // mouse events still reach the link, but the JS handler is
      // the authoritative gate, mirroring how `<button disabled>`
      // works (no pointer-events override; the browser handles
      // the click suppression).
      'a.mar-navigation-link.mar-disabled {',
      '  color: rgba(0, 0, 0, 0.36);',
      '  cursor: not-allowed;',
      '}',
      'a.mar-navigation-link.mar-disabled:hover::before { background: transparent; }',
      'a.mar-navigation-link.mar-disabled:focus-visible::before { background: transparent; }',
      'a.mar-navigation-link.mar-disabled::after { color: rgba(0, 0, 0, 0.18); }',
      // Row-style subtitle: when the subtitle is the second line of a
      // navigation-link\'s title/subtitle pair, render it smaller and
      // closer to the title (the iOS Settings two-line row look).
      // Outside of that context, .mar-subtitle stays at body-text size
      // so subtitles in section bodies (captions, paragraphs) read
      // naturally.
      'a.mar-navigation-link .mar-subtitle {',
      '  font-size: 14px;',
      '  line-height: 1.3;',
      '}',
      'a.mar-navigation-link .mar-vstack { gap: 2px; }',

      // Centered: pure two-axis alignment. It claims whatever space
      // its PARENT provides (flex: 1 inside the page\'s height-
      // propagating column: #mar-root → .mar-nav-stack →
      // .mar-nav-body all pass the viewport height down) and centers
      // the child in it. No own height: in a parent that hugs (a
      // section card) it simply centers horizontally at content
      // height. Used for full-screen Loading / EmptyState / Error.
      '.mar-centered {',
      '  display: flex; flex-direction: column;',
      '  align-items: center; justify-content: center;',
      '  flex: 1 1 auto;',
      '  align-self: stretch;',
      '  width: 100%;',
      '  text-align: center;',
      '}',

      // Spacer: SwiftUI Spacer(). Flex filler that pushes siblings
      // along the parent flex container\'s main axis. Inside an
      // hstack it expands horizontally; inside vstack, vertically.
      '.mar-spacer { flex: 1 1 auto; align-self: stretch; }',

      // Universal sizing: the `width fill` / `height fill` attr
      // classes (set by applyLayoutAttrs on any view). The rules are
      // contextual because what "fill" means depends on the parent\'s
      // flex direction: on the parent\'s MAIN axis the child grows as
      // a flex item (min-size 0 so it truncates instead of
      // overflowing); on the CROSS axis it stretches. So
      // `hstack [] [ text [width fill] a, text [width fill] b ]`
      // yields equal-width columns, and `height fill` in a vstack
      // creates the slack that spacer / centered distribute.
      '.mar-hstack > .mar-w-fill,',
      'a.mar-navigation-link > .mar-w-fill { flex: 1 1 0; min-width: 0; }',
      '.mar-vstack > .mar-w-fill { align-self: stretch; }',
      '.mar-vstack > .mar-h-fill,',
      '.mar-nav-body > .mar-h-fill { flex: 1 1 0; min-height: 0; }',
      '.mar-hstack > .mar-h-fill { align-self: stretch; }',

      // Cross-axis alignment: the stack `align` attr (classes set
      // by applyAlignAttr). Positions HUGGING children in the
      // cross-axis slack; a child with the matching fill has no
      // slack (its stretch rule above wins), so align never resizes.
      // vstack default stays stretch, hstack default stays center:
      // these only fire when the attr is present.
      '.mar-vstack.mar-align-leading  { align-items: flex-start; }',
      '.mar-vstack.mar-align-center   { align-items: center; }',
      '.mar-vstack.mar-align-trailing { align-items: flex-end; }',
      '.mar-hstack.mar-align-top      { align-items: flex-start; }',
      '.mar-hstack.mar-align-center   { align-items: center; }',
      '.mar-hstack.mar-align-bottom   { align-items: flex-end; }',

      // Toggle: mobile-style switch. The native checkbox is hidden
      // via appearance:none and re-drawn as a 51x31 pill with a 27px
      // white thumb that slides on the :checked state. Standard
      // touch-target dimensions.
      '.mar-toggle {',
      '  display: flex; align-items: center; justify-content: space-between;',
      '  gap: 12px;',
      // No padding here: let the parent .mar-section-body > * rule
      // supply 10px 16px (same as text / subtitle / button rows), so
      // the toggle\'s label is inset from the card\'s leading edge
      // and the switch from the trailing edge. Setting `padding`
      // here would override on equal specificity (later wins) and
      // the row would butt against the card borders.
      '  cursor: pointer;',
      '  user-select: none;',
      '}',
      '.mar-toggle-label { flex: 1; min-width: 0; }',
      '.mar-toggle-switch {',
      '  appearance: none; -webkit-appearance: none;',
      '  flex: 0 0 auto;',
      '  width: 51px; height: 31px;',
      '  border-radius: 31px;',
      '  background: #e9e9eb;',
      '  position: relative;',
      '  cursor: pointer;',
      '  transition: background-color 0.2s ease;',
      '  margin: 0;',
      '}',
      '.mar-toggle-switch::before {',
      '  content: "";',
      '  position: absolute;',
      '  top: 2px; left: 2px;',
      '  width: 27px; height: 27px;',
      '  border-radius: 50%;',
      '  background: white;',
      '  box-shadow: 0 3px 8px rgba(0,0,0,0.15), 0 1px 1px rgba(0,0,0,0.06);',
      '  transition: transform 0.2s ease;',
      '}',
      '.mar-toggle-switch:checked { background: #34c759; }',
      '.mar-toggle-switch:checked::before { transform: translateX(20px); }',
      '.mar-toggle-switch:focus-visible {',
      '  outline: none;',
      '  box-shadow: 0 0 0 4px rgba(10, 132, 255, 0.25);',
      '}',
      // Disabled toggle: switch loses its color (or its green if
      // ON), and the label / cursor go inert. `.mar-disabled` is
      // toggled on the <label> wrapper so the label-text faded
      // tone matches the visually-disabled switch.
      '.mar-toggle.mar-disabled { cursor: not-allowed; }',
      '.mar-toggle.mar-disabled .mar-toggle-label {',
      '  color: rgba(0, 0, 0, 0.36);',
      '}',
      '.mar-toggle-switch:disabled {',
      '  opacity: 0.5;',
      '  cursor: not-allowed;',
      '}',

      // ---------- Sheet (modal page sheet) ----------
      //
      // iOS-style sheet: slides up from the bottom, leaves a sliver of
      // the parent visible at the top, dims + blurs the parent behind a
      // backdrop. The DOM structure is:
      //
      //   .mar-sheet-backdrop (full-screen overlay; click → dismiss)
      //     .mar-sheet-panel (the actual sheet card)
      //       .mar-sheet-handle (drag affordance: non-functional v1)
      //       {sheet content}
      //
      // The backdrop is ALWAYS in the DOM (so re-renders can flip
      // open/close without DOM churn) but display: none when closed so
      // it never blocks clicks on the parent.
      '.mar-sheet-backdrop {',
      '  position: fixed; inset: 0;',
      '  background: rgba(0, 0, 0, 0.4);',
      '  -webkit-backdrop-filter: blur(8px);',
      '  backdrop-filter: blur(8px);',
      // ALWAYS display: flex (not toggled to none) so CSS transitions
      // can animate from closed → open. `display: none → flex` is a
      // discrete change the browser never tweens: the sheet would
      // pop in instantly. opacity 0 + pointer-events: none makes the
      // closed state both invisible and click-through.
      '  display: flex; flex-direction: column;',
      '  justify-content: flex-end;',
      '  z-index: 2000;',
      '  opacity: 0;',
      '  pointer-events: none;',
      '  transition: opacity 280ms cubic-bezier(0.32, 0.72, 0, 1);',
      '}',
      '.mar-sheet-backdrop.mar-sheet-open {',
      '  opacity: 1;',
      '  pointer-events: auto;',
      '}',

      // Panel: the actual sheet card.
      // - Page sheet style: leaves ~10vh of the parent visible at top.
      // - Rounded top corners only (bottom flush with screen edge).
      // - White background with subtle inset highlight on top edge.
      // - Slide-up animation with iOS-spec cubic-bezier.
      // Panel sizes to its content (no min-height). Previously had
      // min-height: 50vh which forced a half-screen panel even with
      // a one-line message: large empty area under the content.
      // iOS native page-sheets also size to content; users can drag
      // the handle to expand to detents, which we don't have yet.
      // The 90vh cap keeps an over-long content from pinning the
      // backdrop offscreen.
      //
      // Padding moved here from the children: 20px horizontal,
      // 20px bottom, 4px top (the handle's own 8px margin
      // contributes the rest at the top). Content inside the sheet
      // gets its breathing room without each child having to
      // remember to indent.
      '.mar-sheet-panel {',
      '  background: #f5f5f7;',
      '  border-radius: 14px 14px 0 0;',
      '  max-height: 90vh;',
      '  width: 100%;',
      '  max-width: 1024px;',
      '  margin: 0 auto;',
      '  overflow-y: auto;',
      '  overflow-x: hidden;',
      '  padding: 4px 20px 20px;',
      '  box-shadow:',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8),',
      '    0 -8px 32px rgba(0, 0, 0, 0.12);',
      '  transform: translateY(100%);',
      '  transition: transform 320ms cubic-bezier(0.32, 0.72, 0, 1);',
      '  position: relative;',
      '}',
      '.mar-sheet-open .mar-sheet-panel { transform: translateY(0); }',

      // Drag handle: small pill at the top-center, signals "this can
      // be dragged down to dismiss". Visual affordance only in v1; the
      // drag gesture itself is iOS-native (page sheet) and not yet
      // wired up on web.
      //
      // Negative horizontal margins cancel the panel\'s 20px side
      // padding so the handle stays centered against the panel\'s
      // outer edges, not the content area.
      '.mar-sheet-handle {',
      '  width: 36px; height: 5px;',
      '  background: rgba(0, 0, 0, 0.2);',
      '  border-radius: 2.5px;',
      '  margin: 8px auto 14px;',
      '  flex-shrink: 0;',
      '}',

      // Page-level containers (form, list, nav-body) inside a sheet
      // already supply their own card padding via .mar-section etc;
      // they shouldn\'t double-pad against the sheet\'s own. Cancel
      // the sheet's horizontal padding for them via negative margin
      // so they get full-width treatment like a normal page.
      '.mar-sheet-panel > .mar-form,',
      '.mar-sheet-panel > .mar-list,',
      '.mar-sheet-panel > .mar-nav-body {',
      '  margin-left: -20px;',
      '  margin-right: -20px;',
      '}',

      // Wide viewports: become a FORM SHEET instead of a bottom sheet.
      //
      // A sheet that slides up from the bottom edge is a phone idiom:
      // the thumb is at the bottom and the screen is narrow, so an
      // edge-to-edge panel reads as "a drawer over the page". On a
      // desktop the same panel is a ~1000px-wide, content-tall strip
      // pinned to the bottom of a much taller window: the proportions
      // look accidental, and a short form (one field + two buttons)
      // looks lost in it.
      //
      // So above the tablet breakpoint we switch to what iPadOS/macOS
      // do with a sheet presentation: a narrow card, centered in the
      // window, rounded on all four corners. The min-height stops a
      // one-field form from collapsing into a letterbox: note this is
      // scoped to wide screens ONLY, so the phone bottom sheet keeps
      // sizing to its content (a min-height there would reintroduce
      // the half-empty panel that was removed earlier).
      //
      // The entrance changes too: sliding a centered card up from
      // offscreen would travel the whole window height, so it fades in
      // with a small rise + scale instead. Restated rather than
      // patched, and later in source order, so it wins over the
      // bottom-sheet transform at equal specificity.
      '@media (min-width: 768px) {',
      '  .mar-sheet-backdrop {',
      '    justify-content: center;',
      '    align-items: center;',
      '  }',
      '  .mar-sheet-panel {',
      '    max-width: 460px;',
      '    min-height: 200px;',
      '    max-height: 80vh;',
      '    border-radius: 14px;',
      // The handle is gone below, so the panel supplies its own top
      // padding instead of borrowing the handle's margin.
      '    padding: 20px;',
      '    box-shadow: 0 24px 64px rgba(0, 0, 0, 0.24);',
      '    transform: translateY(8px) scale(0.98);',
      '    opacity: 0;',
      '    transition:',
      '      transform 260ms cubic-bezier(0.32, 0.72, 0, 1),',
      '      opacity 200ms ease-out;',
      '  }',
      '  .mar-sheet-open .mar-sheet-panel {',
      '    transform: none;',
      '    opacity: 1;',
      '  }',
      // Drag-to-dismiss is a touch gesture; a grabber on a centered
      // desktop card would advertise something that isn't there.
      // Escape / backdrop click remain the dismissals.
      '  .mar-sheet-handle { display: none; }',
      '}',

      // A PRESENTED ROUTE (Page.sheet) is a whole screen in a sheet, not
      // a small form, so on wide screens it gets Apple's form-sheet
      // metric (540x620pt) instead of the compact card: a roster or a
      // day's worth of rows needs the extra width to keep its rows from
      // wrapping, and the floor stops a short one from reading as a
      // dialog. On phones nothing changes: a bottom sheet already fills
      // the width it has.
      '@media (min-width: 768px) {',
      '  .mar-page-sheet .mar-sheet-panel {',
      '    max-width: 560px;',
      '    min-height: 320px;',
      '  }',
      '}',

      // Dark mode adjustments: match the rest of the runtime\'s dark
      // theme without re-stating every rule.
      '@media (prefers-color-scheme: dark) {',
      '  .mar-sheet-backdrop { background: rgba(0, 0, 0, 0.6); }',
      '  .mar-sheet-panel {',
      '    background: #1c1c1e;',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.08),',
      '      0 -8px 32px rgba(0, 0, 0, 0.4);',
      '  }',
      '  .mar-sheet-handle { background: rgba(255, 255, 255, 0.3); }',
      '}',

      // ---------- Confirm dialog (UI.confirm) ----------
      //
      // Apple-Music-style destructive confirmation. Structure:
      //
      //   .mar-confirm-backdrop (fixed full-screen + blur)
      //     .mar-confirm-dialog (centered card, max 320px)
      //       .mar-confirm-title (the question)
      //       .mar-confirm-actions (horizontal row of buttons)
      //         .mar-confirm-cancel
      //         .mar-confirm-confirm[.destructive]
      //
      // Unlike .mar-sheet-backdrop, this one is mounted only when
      // the modal is showing: the parent's `case` returns
      // `UI.confirm {...}` only when active, `UI.empty` otherwise.
      // So no opacity-toggle hack needed; we just animate the
      // entrance from the moment the element mounts.
      '.mar-confirm-backdrop {',
      '  position: fixed; inset: 0;',
      '  background: rgba(0, 0, 0, 0.4);',
      '  -webkit-backdrop-filter: blur(8px) saturate(180%);',
      '  backdrop-filter: blur(8px) saturate(180%);',
      '  display: flex; align-items: center; justify-content: center;',
      '  z-index: 2100;',  // above sheet (2000)
      '  animation: mar-confirm-backdrop-in 200ms ease-out;',
      '  padding: 24px;',
      '}',
      '@keyframes mar-confirm-backdrop-in {',
      '  from { opacity: 0; }',
      '  to   { opacity: 1; }',
      '}',
      // Dialog card. Width capped at 320px (Apple Music print). Two
      // stacked sections (title up top, actions below). Subtle
      // shadow so it floats off the backdrop.
      '.mar-confirm-dialog {',
      '  background: rgba(245, 245, 247, 0.96);',
      '  -webkit-backdrop-filter: blur(20px) saturate(180%);',
      '  backdrop-filter: blur(20px) saturate(180%);',
      '  border: 0.5px solid rgba(0, 0, 0, 0.08);',
      '  border-radius: 14px;',
      '  width: 100%;',
      '  max-width: 320px;',
      '  padding: 18px 18px 12px;',
      '  box-shadow:',
      '    0 16px 48px rgba(0, 0, 0, 0.18),',
      '    inset 0 0.5px 0 rgba(255, 255, 255, 0.8);',
      '  animation: mar-confirm-dialog-in 220ms cubic-bezier(0.32, 0.72, 0, 1);',
      '}',
      '@keyframes mar-confirm-dialog-in {',
      '  from { opacity: 0; transform: scale(0.92); }',
      '  to   { opacity: 1; transform: scale(1); }',
      '}',
      '.mar-confirm-title {',
      '  margin: 0 0 16px;',
      '  font: 600 15px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif;',
      '  color: #1d1d1f;',
      '  text-align: center;',
      '  line-height: 1.35;',
      '}',
      '.mar-confirm-actions {',
      '  display: flex; gap: 8px;',
      '}',
      // Both buttons share the same chrome: pill shape, full
      // height. Cancel is system fill, Confirm has the accent color
      // (red when destructive=True, blue otherwise).
      '.mar-confirm-actions > button {',
      '  flex: 1;',
      '  appearance: none;',
      '  border: none;',
      '  padding: 10px 14px;',
      '  border-radius: 980px;',
      '  font: 500 15px -apple-system, BlinkMacSystemFont, "SF Pro Text", sans-serif;',
      '  cursor: pointer;',
      '  transition: background 150ms ease, transform 100ms ease;',
      '  -webkit-tap-highlight-color: transparent;',
      '  touch-action: manipulation;',
      '}',
      '.mar-confirm-actions > button:active { transform: scale(0.97); }',
      '.mar-confirm-cancel {',
      '  background: rgba(0, 0, 0, 0.06);',
      '  color: #1d1d1f;',
      '}',
      '.mar-confirm-cancel:hover { background: rgba(0, 0, 0, 0.10); }',
      // Non-destructive confirm: system blue accent.
      '.mar-confirm-confirm {',
      '  background: #007aff;',
      '  color: #ffffff;',
      '}',
      '.mar-confirm-confirm:hover { background: #0066d6; }',
      // Destructive variant: system red. Same iOS color as
      // .mar-row-delete and SwiftUI .destructive role.
      '.mar-confirm-confirm.destructive {',
      '  background: #ff3b30;',
      '  color: #ffffff;',
      '}',
      '.mar-confirm-confirm.destructive:hover { background: #e0352b; }',

      // Dark mode: switch the dialog card to dark glass + adjust
      // button colors so contrast stays readable.
      '@media (prefers-color-scheme: dark) {',
      '  .mar-confirm-backdrop { background: rgba(0, 0, 0, 0.6); }',
      '  .mar-confirm-dialog {',
      '    background: rgba(44, 44, 46, 0.96);',
      '    border-color: rgba(255, 255, 255, 0.08);',
      '    box-shadow:',
      '      0 16px 48px rgba(0, 0, 0, 0.5),',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.1);',
      '  }',
      '  .mar-confirm-title { color: #ffffff; }',
      '  .mar-confirm-cancel {',
      '    background: rgba(255, 255, 255, 0.10);',
      '    color: #ffffff;',
      '  }',
      '  .mar-confirm-cancel:hover { background: rgba(255, 255, 255, 0.16); }',
      '  .mar-confirm-confirm { background: #0a84ff; }',
      '  .mar-confirm-confirm:hover { background: #0070dd; }',
      '  .mar-confirm-confirm.destructive { background: #ff453a; }',
      '  .mar-confirm-confirm.destructive:hover { background: #e63d34; }',
      '}',

      // ---------- Page transitions ----------
      //
      // The mar runtime wraps a navigation\'s DOM swap in
      // `document.startViewTransition(swap)` when the View
      // Transitions API exists (Chrome 111+, Safari 18+,
      // Firefox 129+ behind flag). The browser captures the old
      // tree as `::view-transition-old(root)` and the new tree
      // as `::view-transition-new(root)`; we drive the actual
      // animation here with CSS keyframes selected by the
      // `data-mar-nav-dir` attribute the runtime stamps on
      // <html> before the swap.
      //
      // The keyframes mirror iOS NavigationStack:
      //   forward (push): new slides in from the right, old
      //     parallaxes slightly left and dims.
      //   back (pop)     - reversed: new slides in from the left,
      //     old slides out to the right.
      //
      // Timing curve is the standard "page push" easing:
      // cubic-bezier(0.32, 0.72, 0, 1): applied at 400ms. Below
      // ~320ms the slide reads as a snap; above ~480ms it starts
      // feeling sluggish on a browser.
      '@keyframes mar-slide-in-right { from { transform: translateX(100%); } }',
      '@keyframes mar-slide-out-left  {  to  { transform: translateX(-25%); opacity: 0.5; } }',
      '@keyframes mar-slide-in-left  { from { transform: translateX(-25%); opacity: 0.5; } }',
      '@keyframes mar-slide-out-right {  to  { transform: translateX(100%); } }',
      'html[data-mar-nav-dir="forward"]::view-transition-old(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-out-left;',
      '}',
      'html[data-mar-nav-dir="forward"]::view-transition-new(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-in-right;',
      '}',
      'html[data-mar-nav-dir="back"]::view-transition-old(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-out-right;',
      '}',
      'html[data-mar-nav-dir="back"]::view-transition-new(root) {',
      '  animation: 400ms cubic-bezier(0.32, 0.72, 0, 1) both mar-slide-in-left;',
      '}',
      // ----- Fade transition (replace / replaceFresh) -----
      //
      // Cross-fade with a subtle scale-up on the incoming view. Used
      // when the navigation isn't a stack movement but a context
      // change (logout, sign-in completed, auth-expired redirect).
      //
      // The visual idiom is what iOS uses for Sign in with Apple
      // confirmations, Apple Pay completion, FaceID success → app
      // unlock, and the Photos / Apple Music "you switched modes"
      // transitions: outgoing dissolves while the new content
      // settles in from slightly smaller (0.96 → 1.0). No
      // horizontal motion → reads as "new context", not "next/prev
      // screen". Duration sits at 280ms: fast enough to feel
      // responsive after a tap, slow enough that the scale registers
      // as deliberate rather than a flash.
      //
      // Easing is the same iOS curve used for the slide so the
      // perceived "personality" stays consistent across nav verbs.
      '@keyframes mar-fade-out { to { opacity: 0; } }',
      '@keyframes mar-fade-in-scale {',
      '  from { opacity: 0; transform: scale(0.96); }',
      '  to   { opacity: 1; transform: scale(1); }',
      '}',
      'html[data-mar-nav-dir="fade"]::view-transition-old(root) {',
      '  animation: 220ms cubic-bezier(0.32, 0.72, 0, 1) both mar-fade-out;',
      '}',
      'html[data-mar-nav-dir="fade"]::view-transition-new(root) {',
      '  animation: 280ms cubic-bezier(0.32, 0.72, 0, 1) both mar-fade-in-scale;',
      '  transform-origin: center center;',
      '}',
      // Dev dock pin. The dock element carries
      // `view-transition-name: mar-dev-dock` (see createDevDock),
      // which splits it out of the `root` snapshot group into its
      // own. `animation: none` here keeps that group static
      // through the page transition, so the bottom-right widget
      // stays anchored while the page slides under it. Without
      // this rule the browser would default to a cross-fade,
      // which on a static element with identical old/new content
      // looks like a brief flicker.
      '::view-transition-group(mar-dev-dock),',
      '::view-transition-old(mar-dev-dock),',
      '::view-transition-new(mar-dev-dock) {',
      '  animation: none !important;',
      '}',
      // Accessibility: kill the slide for users who set
      // `prefers-reduced-motion`. The runtime also skips the
      // startViewTransition call entirely in that case, but the
      // CSS guard belongs here too for any non-runtime triggers
      // (e.g. dev-tools forcing a transition).
      '@media (prefers-reduced-motion: reduce) {',
      '  ::view-transition-old(root), ::view-transition-new(root) {',
      '    animation: none !important;',
      '  }',
      '  .mar-nav-inline-title { transition: none; }',
      // The bar is always solid; only the hairline transitions, and dropping
      // that fade under reduced-motion costs nothing.
      '  .mar-nav-toolbar-row { transition: none; }',
      '}',

      // Dark mode: iOS 26 "Liquid Glass" on a near-black graphite
      // page. The gradient hints at light coming from above; the
      // glass surfaces (cards, pills) tint that light bluish-warm
      // via subtle saturation in the backdrop filter.
      '@media (prefers-color-scheme: dark) {',
      // Match <html> to the darkest stop of the body gradient. Same
      // rationale as the light-mode rule above: bottom-bounce
      // overscroll and view-transition gaps would otherwise flash
      // white through the dark page. Also add an explicit
      // ::selection so highlighted text stays legible, the
      // browser's default selection color on macOS is a light
      // blue/grey that washes out the page's #f5f5f7 text into
      // near-invisibility against the highlight.
      '  html { background-color: #161618; }',
      '  ::selection { background: rgba(10, 132, 255, 0.55); color: #ffffff; }',
      '  body {',
      '    background: linear-gradient(180deg, #232326 0%, #1d1d1f 60%, #161618 100%);',
      '    color: #f5f5f7;',
      '  }',
      // Pills (back button, trailing nav buttons, inline hstack
      // actions) all share one lifted-slate fill. The inline title is
      // deliberately NOT in this group: it is bare text and reads
      // against the bar itself instead of carrying chrome of its own.
      // Same split as light mode: control wears a container, label
      // does not.
      '  .mar-nav-back, .mar-nav-side button, .mar-nav-side a.mar-inline {',
      '    background: #2c2c2e;',
      '    border: 0.5px solid rgba(255, 255, 255, 0.14);',
      '    box-shadow: none;',
      '  }',
      // The bar = the page's TOP color = the dark theme-color, all #232326
      // (the top stop of the body gradient). Setting it to the gradient's DARK
      // stop (#161618) was the old dark-mode seam: a near-black strip over a
      // lighter graphite page. The status bar sits where the page is lightest,
      // so the bar has to be that lightest color. The hairline flips light
      // (a dark line on a dark bar vanishes).
      '  .mar-nav-toolbar-row { background: rgba(35, 35, 38, 0.6); }',
      '  .mar-nav-toolbar-row.mar-nav-scrolled { border-bottom-color: rgba(255, 255, 255, 0.10); }',
      '  .mar-nav-inline-title { color: #f5f5f7; }',
      // Label color, not action color: see the light-mode note.
      '  .mar-nav-back, .mar-nav-side button, .mar-nav-side a.mar-inline { color: #f5f5f7; }',
      '  @media (hover: hover) { .mar-nav-back:hover { background: #3a3a3c; } }',
      '  @media (hover: hover) { .mar-nav-side button:hover, .mar-nav-side a.mar-inline:hover { background: #3a3a3c; } }',
      // Section card in dark glass: translucent slate that lets
      // the gradient bleed through under the blur.
      '  .mar-section-body {',
      '    background: rgba(44, 44, 46, 0.7);',
      '    border: 0.5px solid rgba(255, 255, 255, 0.06);',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.08),',
      '      0 8px 24px rgba(0, 0, 0, 0.35);',
      '  }',
      '  .mar-section-body > * { border-bottom-color: rgba(255, 255, 255, 0.08); }',
      '  .mar-section-body > *:last-child { border-bottom: none; }',
      '  .mar-section-header, .mar-section-footer { color: #86868b; }',
      '  .mar-textfield::placeholder { color: #86868b; }',
      '  .mar-textfield {',
      '    border-color: rgba(255, 255, 255, 0.12);',
      '    background: rgba(255, 255, 255, 0.04);',
      '    color: #f5f5f7;',
      '  }',
      '  .mar-textfield:focus {',
      '    background: rgba(255, 255, 255, 0.06);',
      '    border-color: rgba(10, 132, 255, 0.6);',
      '    box-shadow: 0 0 0 4px rgba(10, 132, 255, 0.18);',
      '  }',
      // Disabled textfield in dark mode. The light-mode rule uses
      // `color: rgba(0,0,0,0.36)`, 36% black on a near-black
      // surface in dark mode lands the text essentially invisible.
      // Operator reported they couldn't read the email address
      // they had just typed while the "Sending…" request was in
      // flight. Mirror the dimming PROPORTIONALLY against white,
      // not by reusing the light-mode rgba: text goes to ~50%
      // white (still clearly readable), placeholder to ~30%, and
      // the surface stays the same translucent slate as the
      // enabled state so the field's outline doesn't shift.
      '  .mar-textfield:disabled {',
      '    background: rgba(255, 255, 255, 0.04);',
      '    color: rgba(255, 255, 255, 0.5);',
      '    border-color: rgba(255, 255, 255, 0.08);',
      '  }',
      '  .mar-textfield:disabled::placeholder { color: rgba(255, 255, 255, 0.3); }',
      // Primary CTAs stay solid blue, just brighter for legibility
      // on the darker surface.
      '  .mar-button {',
      '    background: #0a84ff; color: white;',
      '  }',
      '  .mar-button:disabled {',
      '    background: #3a3a3c; color: rgba(255, 255, 255, 0.4);',
      '  }',
      // hover tint gated to real pointers (iOS sticky-hover fix), same as
      // the light-mode rules above
      '  @media (hover: hover) {',
      '    .mar-button:hover { background: #2997ff; }',
      '    .mar-button:disabled:hover { background: #3a3a3c; }',
      '  }',
      // Secondary (hstack) buttons in dark mode: same glass treatment
      // with blue text. The drag handle and the delete circle are built
      // by the runtime with their own classes, so they never carry
      // `mar-button` and keep their own chrome.
      '  .mar-hstack > .mar-button {',
      '    background: rgba(255, 255, 255, 0.08);',
      '    border: 0.5px solid rgba(255, 255, 255, 0.12);',
      '    box-shadow:',
      '      inset 0 0.5px 0 rgba(255, 255, 255, 0.15),',
      '      0 2px 8px rgba(0, 0, 0, 0.3);',
      '    color: #0a84ff;',
      '  }',
      '  .mar-hstack > .mar-button:disabled {',
      '    background: rgba(255, 255, 255, 0.04);',
      '    color: rgba(255, 255, 255, 0.3);',
      '    box-shadow: none;',
      '  }',
      '  @media (hover: hover) {',
      '    .mar-hstack > .mar-button:hover { background: rgba(255, 255, 255, 0.14); }',
      '    .mar-hstack > .mar-button:disabled:hover { background: rgba(255, 255, 255, 0.04); }',
      '  }',
      // Toggle in dark mode: off-track turns dark grey, on-track
      // keeps the same iOS green; thumb stays white.
      '  .mar-toggle-switch { background: #39393d; }',
      '  .mar-toggle-switch:checked { background: #34c759; }',
      '}',

      // ===== onDelete per-row affordance (edit-mode only) =====
      //
      // Single visual mode: when the section is in delete-editing
      // state, every row carries a red `−` circle at its LEFT (iOS
      // Mail/Notes/Reminders edit-mode chrome). Outside edit mode
      // the button isn't rendered at all: see
      // attachRowDeleteAffordance, so this CSS doesn't need to
      // cover that case.
      //
      // We use compound selectors (`.mar-section-body
      // .mar-row-delete`) for size + shape so we win the cascade
      // against the generic `.mar-hstack > button { flex: 0 0
      // auto; }` rule defined earlier; the row that hosts the
      // button IS an `.mar-hstack` in edit mode (Mar code wraps
      // the row in `hstack [key] [text ...]` to attach the
      // reorder key), and without enough specificity the chrome
      // styles bleed in and turn the disc into a stretched pill.
      '.mar-section-body .mar-row-delete {',
      '  flex: 0 0 22px;',
      '  width: 22px;',
      '  min-width: 22px;',
      '  height: 22px;',
      '  min-height: 22px;',
      '  border-radius: 50%;',
      '  border: none;',
      '  padding: 0;',
      '  background: #ff3b30;',
      '  color: #ffffff;',
      '  display: inline-flex;',
      '  align-items: center;',
      '  justify-content: center;',
      '  cursor: pointer;',
      '  -webkit-tap-highlight-color: transparent;',
      '  transition: transform 150ms ease;',
      '  box-shadow: none;',
      '}',
      '.mar-section-body .mar-row-delete:hover { background: #e0352b; }',
      '.mar-section-body .mar-row-delete:active { transform: scale(0.92); }',
      // Row layout in edit-with-delete mode: row becomes a flex
      // container so the delete button (order:-1) can sit on the
      // left of whatever the row's normal content is. The
      // negative margin pokes the disc out into the section
      // card\'s padding gutter: same offset iOS uses.
      '.mar-section-delete-edit > * {',
      '  display: flex;',
      '  align-items: center;',
      '  gap: 12px;',
      '}',
      '.mar-section-delete-edit > * > .mar-row-delete {',
      '  order: -1;',
      '  margin-left: -4px;',
      '}',
      // Exit animation is driven imperatively via Web Animations
      // API in attachRowDeleteAffordance: see that function for
      // the keyframes (height collapse + slide-left + fade). The
      // imperative path needs concrete `from` keyframes (current
      // height, current padding), so the animation has to be
      // built per-row at click time, not via a fixed CSS class.
      // Dark mode: tone the red down, full system-red against a
      // dark background reads harsh.
      '@media (prefers-color-scheme: dark) {',
      '  .mar-section-body .mar-row-delete { background: #ff453a; }',
      '  .mar-section-body .mar-row-delete:hover { background: #e63d34; }',
      '}',
    ].join('\n');
    document.head.appendChild(style);
  }

  // domTagFor returns the HTML element name for a VView tag.
  // Tags map to semantic HTML so dark mode / accessibility work out of
  // the box; CSS in ensureUIStyles() gives each a SwiftUI-flavored look.
  function domTagFor(viewTag) {
    switch (viewTag) {
      case 'text':            return 'span';
      case 'title':           return 'h1';
      case 'subtitle':        return 'h2';
      case 'errorText':       return 'p';
      case 'image':           return 'img';
      case 'paragraph':       return 'p';
      // span: <a> when an inlineLink attr is present, <span>
      // otherwise. createDOM handles the link case directly because
      // the choice depends on attrs, not just tag. Leaving the
      // default here at 'span' keeps the no-link path simple.
      case 'span':            return 'span';
      case 'button':          return 'button';
      case 'navigationLink':  return 'a';
      case 'navigationStack': return 'main';
      case 'form':            return 'form';
      case 'uiList':          return 'ul';
      case 'uiSection':       return 'section';
      case 'uiKeyedList':     return 'section';
      case 'hstack':          return 'div';
      case 'vstack':          return 'div';
      case 'textField':       return 'input';
      case 'textArea':        return 'textarea';
      case 'picker':          return 'div';
      case 'datePicker':      return 'input';
      case 'canvas':          return 'canvas';
      case 'spacer':          return 'div';
      case 'toggle':          return 'label';
      case 'sheet':           return 'div';  // .mar-sheet-backdrop wrapper
      case 'confirmDialog':   return 'div';  // .mar-confirm-backdrop wrapper
      default:                return 'div';
    }
  }

  // datePicker <-> <input type="date">. The bound value is a Date (datePicker
  // is pure: the program owns the value and seeds "today" via
  // `Cmd.perform GotToday Date.today`), and the control speaks a bare
  // "YYYY-MM-DD", the Date's own text, so no time zone is read on the way in
  // or out. Both clients offer the days from 1582-10-15, the first day of the
  // Gregorian calendar, to 9999-12-31: the phone's picker takes only
  // Foundation's calendar, which labels the days before it by the Julian
  // calendar (MarCivil.swift). A Date before the first offered day shows as
  // that day, as on the phone, whose picker has no empty state.
  const PICKER_FIRST_DAY = daysFromCivil(1582, 10, 15);
  const PICKER_FIRST_LABEL = '1582-10-15';

  function dateInputValue(view) {
    const a = (view.attrs || []).find(x => x.name === 'value');
    const dt = a && a.value;
    if (!dt || typeof dt.days !== 'number') return '';
    return dt.days < PICKER_FIRST_DAY ? PICKER_FIRST_LABEL : formatDateISO(dt);
  }

  // The Date a picked text stands for, or null for a text that is not a day
  // the control offers (an emptied field, a typed 1500-01-01).
  function parseDateInput(str) {
    const days = parseDateISO(str || '');
    if (days === null || days < PICKER_FIRST_DAY) return null;
    return dateOf(str, days);
  }

  // The input fires a change on every keystroke, carrying the field's text,
  // so a year being typed passes through texts that are not offered days
  // (0001, 0019, 0199). Those are left alone; an offered day is dispatched.
  function dateInputChanged(e, dispatch) {
    const v = e.__marView;
    const picked = parseDateInput(e.value);
    if (!picked || !v || v.msg == null || !dispatch) return;
    dispatch(apply(v.msg, picked));
  }

  // Leaving the field puts back the day the model holds, so an emptied or
  // half-typed field does not stay on screen: the phone's picker always
  // shows a day.
  function dateInputLeft(e) {
    const v = e.__marView;
    if (!v) return;
    const shown = dateInputValue(v);
    if (e.value !== shown) e.value = shown;
  }

  function hasFocus(node) {
    return typeof document !== 'undefined' && document.activeElement === node;
  }

  // localOffsets answers the device's offset from UTC, in ms, at any instant,
  // read from the time zone's name in Intl's longOffset form: "GMT",
  // "GMT-03:00", or "GMT-03:06:28" for a local mean time. Intl rather than
  // Date's local getters, which V8 answers from a cache that skips a
  // daylight-saving period shorter than a few weeks.
  function localOffsets() {
    const format = new Intl.DateTimeFormat('en-US', { timeZoneName: 'longOffset' });
    return (ms) => {
      const name = format.formatToParts(ms).find(p => p.type === 'timeZoneName').value;
      const m = /^GMT(?:([+\u2212-])(\d{2}):(\d{2})(?::(\d{2}))?)?$/.exec(name);
      if (!m) throw new Error('Date.today: the time zone offset ' + name + ' is not one Mar reads');
      if (!m[1]) return 0;
      const size = ((+m[2] * 60 + +m[3]) * 60 + +(m[4] || 0)) * 1000;
      return m[1] === '+' ? size : -size;
    };
  }

  // localDay is the day the device's wall clock shows at ms: the instant moved
  // by the offset in force there, as the phone's MarCivil.localDay.
  function localDay(ms, offset) {
    return Math.floor((ms + offset(ms)) / MS_PER_DAY);
  }

  // styleInlineSpan syncs an inline-run element (from `span`) to its view:
  // the CSS classes (bold / italic / strike / code), the link href, and the
  // text. Shared by createDOM (first paint) and patchDOM (re-render) so a
  // span's TEXT and STYLE both track the model: without it, a dynamic span
  // inside a paragraph froze at its first value (text was set once in
  // createDOM and never patched).
  function styleInlineSpan(e, view) {
    ensureUIStyles();
    const classes = ['mar-inline'];
    let href = null;
    for (const a of (view.attrs || [])) {
      switch (a.name) {
        case 'inlineBold':          classes.push('mar-inline-bold'); break;
        case 'inlineItalic':        classes.push('mar-inline-italic'); break;
        case 'inlineStrikethrough': classes.push('mar-inline-strike'); break;
        case 'inlineCode':          classes.push('mar-inline-code'); break;
        case 'inlineLink':          href = a.value && a.value.s; break;
      }
    }
    if (href) {
      // External target: link inline text always opens off-site (there's no
      // "internal inline link" primitive; navigationLink covers internal). New
      // tab + noopener so the source page can't be poked at.
      classes.push('mar-inline-link');
      e.setAttribute('href', href);
      e.setAttribute('target', '_blank');
      e.setAttribute('rel', 'noopener noreferrer');
    } else {
      // Re-render may have dropped the link: clear the anchor attrs so a
      // once-link span doesn't keep a stale href.
      e.removeAttribute('href');
      e.removeAttribute('target');
      e.removeAttribute('rel');
    }
    e.className = classes.join(' ');
    e.textContent = view.text;
  }

  function createDOM(view) {
    if (!view || view.k !== 'V') {
      return document.createElement('span');
    }
    if (view.tag === 'empty') {
      // Use a no-op span as a stable placeholder so diff has a 1:1 mapping.
      const e = document.createElement('span');
      e.style.display = 'none';
      setMarView(e, view);
      return e;
    }
    // span: tag depends on attrs (link → <a>, otherwise <span>).
    // Resolve up-front so we don\'t create a wrong element and
    // re-wrap it. All the styling lives in CSS classes added below.
    let tag = domTagFor(view.tag);
    if (view.tag === 'span') {
      const linkAttr = (view.attrs || []).find(a => a.name === 'inlineLink');
      if (linkAttr) tag = 'a';
    }
    const e = document.createElement(tag);
    setMarView(e, view);
    switch (view.tag) {
      case 'text':
        e.textContent = view.text;
        break;
      case 'paragraph':
        // Block of flowing inline content. Children are `span`
        // VViews; createDOM recurses to build the right inline
        // elements. Class drives the block-level styling (margin,
        // line-height); the children carry their own per-run
        // styling.
        ensureUIStyles();
        e.className = 'mar-paragraph';
        for (const child of (view.children || [])) {
          e.appendChild(createDOM(child));
        }
        break;
      case 'span': {
        // Inline run. Classes (bold / italic / strike / code), link href, and
        // text all come from styleInlineSpan: the same helper patchDOM uses,
        // so a dynamic span reconciles on re-render. The span-vs-anchor tag was
        // already resolved above from the link attr.
        styleInlineSpan(e, view);
        break;
      }
      case 'title':
        ensureUIStyles();
        e.className = 'mar-title';
        e.textContent = view.text;
        break;
      case 'subtitle':
        ensureUIStyles();
        e.className = 'mar-subtitle';
        e.textContent = view.text;
        break;
      case 'errorText':
        // Red + semi-bold via .mar-error-text. role="alert" so
        // assistive tech announces the message when it appears
        // (errors typically arrive after an action, not at page
        // load: the live-region announcement is the whole point).
        ensureUIStyles();
        e.className = 'mar-error-text';
        e.setAttribute('role', 'alert');
        e.textContent = view.text;
        break;
      case 'image':
        // <img> from src + alt. alt is always present (required record
        // field on the Mar side); "" is the decorative escape. lazy +
        // async so off-screen images don't block first paint.
        ensureUIStyles();
        e.className = 'mar-image';
        e.setAttribute('src', getAttr(view, 'src'));
        e.setAttribute('alt', getAttr(view, 'alt'));
        e.setAttribute('loading', 'lazy');
        e.setAttribute('decoding', 'async');
        applyImageAttrs(e, view);
        break;
      case 'button':
        // Every button carries `mar-button`, and the stylesheet keys on
        // that class rather than on where the button sits. The rules used
        // to read `.mar-section-body > button`, so a button one level
        // deeper (inside a vstack, a form, a sheet header) matched
        // nothing and rendered as a bare browser button with no press
        // state. Position still selects the VARIANT (the compact glass
        // pill inside an hstack), which is a real difference; it no
        // longer decides whether the button is styled at all.
        e.className = 'mar-button';
        e.textContent = view.text;
        applyDisabledAttr(e, view);
        attachClickDispatcher(e);
        break;
      case 'navigationLink':
        // Tappable navigation: full-width anchor wrapping the
        // child view DOM. The delegated SPA-routing interceptor
        // in mountPages catches the click via `[href^="/"]` and
        // pushes through history.pushState instead of doing a
        // full reload.
        //
        // `disabled` here can't use the HTML disabled property:
        // <a> doesn't have one. Instead we mark the link with
        // aria-disabled + a CSS class; the delegated click handler
        // (onLinkClick) reads __marView.attrs and skips
        // navigation when disabled, and the CSS dims + sets
        // pointer-events: none so hover stays inert.
        //
        // `tabindex="0"` forces the link into the keyboard tab
        // order. Without it, Safari on macOS skips <a> elements by
        // default (it considers links "content", not "controls",
        // unless the user enables Settings > Advanced > "Press
        // Tab to highlight each item on a webpage"). For an app
        // where navigationLink IS a primary control: Settings-row
        // style "go to next screen": we want every browser to
        // tab through it without the user fiddling with system
        // preferences. tabindex="0" is a no-op in Chrome/Firefox
        // (they tab to links anyway) and the right knob for Safari.
        ensureUIStyles();
        e.className = 'mar-navigation-link';
        e.setAttribute('href', getAttr(view, 'href'));
        e.setAttribute('tabindex', '0');
        applyAnchorDisabled(e, view);
        for (const child of view.children) {
          e.appendChild(createDOM(child));
        }
        break;
      // ---------- UI.* (SwiftUI-style) ----------
      //
      // Each UI container uses a STABLE child-DOM structure so patchDOM
      // can update text/children in place without recreating nodes.
      // Critical for inputs: replacing a node mid-keystroke kills focus
      // and selection, which means the user types "teste" and only "t"
      // makes it through (every re-render after the first character
      // recreates the input, blurring it).
      case 'navigationStack':
        // <main>
        //   <div.mar-nav-toolbar-row> ← always present; pinned bar
        //   <header.mar-nav-bar>      ← always present; large title
        //   <div.mar-nav-body>        ← stable wrapper, children diffed
        // </main>
        ensureUIStyles();
        e.className = 'mar-nav-stack';
        for (const chromeEl of buildNavigationChrome(view)) e.appendChild(chromeEl);
        {
          const body = document.createElement('div');
          body.className = 'mar-nav-body';
          for (const c of view.children) body.appendChild(createDOM(c));
          e.appendChild(body);
        }
        break;
      case 'form':
        // <form>{children}</form>: children diffed positionally.
        // Suppress the default Enter-submits behavior; per-field
        // submit is handled by the UI.submit attr.
        ensureUIStyles();
        e.className = 'mar-form';
        e.addEventListener('submit', (ev) => ev.preventDefault());
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'uiList':
        // <div.mar-list>{sections-or-rows}</div>: always a div
        // wrapper with children rendered directly. Avoids <li>
        // wrapping (semantic noise without payoff for sectioned
        // lists). No reorder semantics on the list itself: those
        // live on its constituent `section`s (see the uiSection
        // ctor for handle wiring + keyboard a11y).
        ensureUIStyles();
        e.className = 'mar-list';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'uiSection':
      case 'uiKeyedList': {
        // <section>
        //   <h2.mar-section-header>  ← always present; hidden if empty
        //   <div.mar-section-body>   ← stable wrapper, children diffed
        //   <p.mar-section-footer>   ← always present; hidden if empty
        // </section>
        //
        // uiSection and uiKeyedList share the same visual chrome
        // (rounded card with header + footer). The semantic
        // difference is at the type level: uiKeyedList children
        // each carry a `key` attr (injected by `UI.keyed`), while
        // uiSection children don\'t. The reconciler reads the key
        // for keyed children automatically: no special-casing
        // needed here.
        ensureUIStyles();
        e.className = 'mar-section';
        const headerText = getAttr(view, 'header');
        const footerText = getAttr(view, 'footer');
        const h = document.createElement('h2');
        h.className = 'mar-section-header';
        h.textContent = headerText;
        if (!headerText) h.style.display = 'none';
        e.appendChild(h);
        const body = document.createElement('div');
        body.className = 'mar-section-body';
        // Reorder support: driven by the `onMove` attr's editing
        // flag. In edit mode each row gets a drag handle (mouse /
        // touch) AND becomes keyboard-focusable with grab + arrow
        // semantics (screen-reader friendly). Both
        // gestures dispatch the same onMove(from, to) handler.
        const onMoveAttrS = getAttrRaw(view, 'onMove');
        const editingS = !!(onMoveAttrS && onMoveAttrS.editing);
        const handlerS = onMoveAttrS && onMoveAttrS.handler;
        const onDeleteAttrS = getAttrRaw(view, 'onDelete');
        const deleteEditingS = !!(onDeleteAttrS && onDeleteAttrS.editing);
        const deleteHandlerS = onDeleteAttrS && onDeleteAttrS.handler;
        if (editingS) body.classList.add('mar-section-editing');
        // `.mar-section-delete-edit` flips the section body into the
        // layout that hosts a per-row delete affordance on the left
        // of each row (iOS edit-mode minus circle). Only on when
        // the app explicitly opts into onDelete + editing=true; in
        // browse mode the rows are plain (no destructive control
        // surface).
        if (deleteEditingS) body.classList.add('mar-section-delete-edit');
        const totalS = view.children.length;
        for (let i = 0; i < totalS; i++) {
          const childEl = createDOM(view.children[i]);
          if (editingS) {
            ensureRowEditAffordances(childEl, i, totalS);
          }
          if (deleteHandlerS) {
            attachRowDeleteAffordance(childEl, i, deleteHandlerS, deleteEditingS);
          }
          body.appendChild(childEl);
        }
        if (editingS && handlerS) {
          attachListReorderDrag(body, handlerS);
          // Live-region hosted on the section wrapper (`e`), not on
          // the body. See the function for why, TLDR: appending to
          // body shifts the last actual row out of `:last-child` and
          // it gains a hairline border, reading as a phantom extra
          // row.
          attachListReorderKeyboard(body, handlerS, e);
        }
        e.appendChild(body);
        const f = document.createElement('p');
        f.className = 'mar-section-footer';
        f.textContent = footerText;
        if (!footerText) f.style.display = 'none';
        e.appendChild(f);
        break;
      }
      case 'hstack':
        ensureUIStyles();
        e.className = 'mar-hstack';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'vstack':
        ensureUIStyles();
        e.className = 'mar-vstack';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'textField': {
        ensureUIStyles();
        e.className = 'mar-textfield';
        applyInputKind(e, view);
        applySizing(e, view);
        const ph = getAttr(view, 'placeholder');
        if (ph) e.setAttribute('placeholder', ph);
        e.value = view.text;
        // `<input disabled>` natively suppresses focus, typing, AND
        // the `input` / `keydown` events, so neither
        // attachInputDispatcher nor attachSubmitDispatcher needs an
        // extra guard once we sync the DOM property here.
        applyDisabledAttr(e, view);
        attachInputDispatcher(e);
        attachSubmitDispatcher(e);
        break;
      }
      case 'textArea': {
        // Same dispatcher wiring as textField: the renderer just
        // emits a <textarea> instead of <input>. We reuse the
        // textfield CSS class so the borders / focus ring / padding
        // line up with neighboring textField rows; a small
        // textArea-specific override (min-height, line-height)
        // lives alongside.
        ensureUIStyles();
        e.className = 'mar-textfield mar-textarea';
        const ph = getAttr(view, 'placeholder');
        if (ph) e.setAttribute('placeholder', ph);
        e.value = view.text;
        // A reasonable default: three lines of room without
        // committing to a tall fixed block. User code can override
        // via `[ height (lines N) ]` (applySizing sets rows).
        if (!e.hasAttribute('rows')) e.setAttribute('rows', '3');
        applySizing(e, view);
        applyDisabledAttr(e, view);
        attachInputDispatcher(e);
        attachSubmitDispatcher(e);
        break;
      }
      case 'datePicker': {
        // Native <input type="date">. The browser supplies the
        // calendar popover; we reuse the textfield chrome so the row
        // lines up with neighboring textField / picker rows, and sync
        // the value as the Date's YYYY-MM-DD. A change that reads as an
        // offered day dispatches onChange with it, the same shape as
        // picker / toggle (dateInputChanged); leaving the field puts
        // back the model's day (dateInputLeft).
        ensureUIStyles();
        e.className = 'mar-textfield mar-datepicker';
        e.type = 'date';
        e.min = PICKER_FIRST_LABEL;
        e.max = '9999-12-31';
        applySizing(e, view);
        e.value = dateInputValue(view);
        applyDisabledAttr(e, view);
        e.addEventListener('change', () => dateInputChanged(e, currentDispatch));
        e.addEventListener('blur', () => dateInputLeft(e));
        break;
      }
      case 'picker': {
        // <div.mar-picker>
        //   <select.mar-picker-select>
        //     <option value="0">Label1</option>
        //     <option value="1" selected>Label2</option>
        //     ...
        //   </select>
        //   <span.mar-picker-chevron aria-hidden>›</span>
        // </div>
        //
        // The wrapping div lets us layer a custom chevron over the
        // native select without losing the platform's accessible
        // dropdown UI (keyboard nav, screen-reader announcements,
        // mobile native popover). The chevron is decorative: the
        // <select> itself is the focusable / interactive surface.
        ensureUIStyles();
        e.className = 'mar-picker';
        applySizing(e, view);
        const select = document.createElement('select');
        select.className = 'mar-picker-select';
        renderPickerOptions(select, view);
        // `<select disabled>` natively blocks the dropdown from
        // opening and suppresses change events, so we sync the
        // property on the inner element, not the wrapping div.
        // Mirror the disabled state onto the wrapper too so CSS
        // can grey-out the chevron + text via :has() / class.
        applyDisabledAttr(select, view);
        e.classList.toggle('mar-disabled', isDisabled(view));
        e.appendChild(select);
        // Inline SVG renders consistently across browsers / fonts:
        // unlike a unicode chevron glyph which Safari + Chrome
        // sometimes render as a thin stub or fail to size with the
        // surrounding CSS font-size. Two stacked triangles mirror
        // SwiftUI's Picker(.menu) "select-up-down" indicator.
        const chevron = document.createElement('span');
        chevron.className = 'mar-picker-chevron';
        chevron.setAttribute('aria-hidden', 'true');
        chevron.innerHTML =
          '<svg xmlns="http://www.w3.org/2000/svg" width="14" height="18" ' +
          'viewBox="0 0 14 18" fill="none" stroke="currentColor" ' +
          'stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
          '<polyline points="3,7 7,3 11,7"/>' +
          '<polyline points="3,11 7,15 11,11"/>' +
          '</svg>';
        e.appendChild(chevron);
        select.addEventListener('change', () => {
          const v = e.__marView;
          if (!currentDispatch || !v || v.msg == null) return;
          const optionsAttr = v.attrs.find(a => a.name === 'options');
          if (!optionsAttr || !optionsAttr.value || optionsAttr.value.k !== 'L') return;
          const idx = parseInt(select.value, 10);
          if (!Number.isFinite(idx)) return;
          const picked = optionsAttr.value.xs[idx];
          if (picked === undefined) return;
          currentDispatch(apply(v.msg, picked));
        });
        break;
      }
      case 'centered':
        // Pure two-axis alignment: fills whatever space the PARENT
        // provides (flex: 1 in the page's height-propagating column
        //: it never invents a height of its own) and centers the
        // child in it. Full-screen states (Loading, EmptyState, …).
        ensureUIStyles();
        e.className = 'mar-centered';
        for (const c of view.children) e.appendChild(createDOM(c));
        break;
      case 'spacer':
        // Flex filler: the parent flex container (hstack / vstack
        // / section-body) absorbs it and pushes siblings apart.
        ensureUIStyles();
        e.className = 'mar-spacer';
        break;
      case 'toggle': {
        // <label.mar-toggle>
        //   <span.mar-toggle-label>{text}</span>
        //   <input.mar-toggle-switch type=checkbox>
        // </label>
        //
        // Stable structure so patchDOM can update text + checked
        // state in place without recreating nodes (the checkbox's
        // styled track/thumb survives re-renders that way).
        ensureUIStyles();
        e.className = 'mar-toggle';
        const labelSpan = document.createElement('span');
        labelSpan.className = 'mar-toggle-label';
        labelSpan.textContent = view.text;
        e.appendChild(labelSpan);
        const sw = document.createElement('input');
        sw.type = 'checkbox';
        sw.className = 'mar-toggle-switch';
        sw.checked = toggleIsOn(view);
        // `<input type=checkbox disabled>` natively swallows clicks
        // and change events, so the dispatcher below never fires
        // while disabled. Sync the class on the <label> so CSS can
        // fade the label text + cursor along with the switch.
        applyDisabledAttr(sw, view);
        e.classList.toggle('mar-disabled', isDisabled(view));
        sw.addEventListener('change', () => {
          const v = e.__marView;
          if (!currentDispatch || !v || v.msg == null) return;
          currentDispatch(apply(v.msg, VBool(sw.checked)));
        });
        e.appendChild(sw);
        break;
      }
      case 'sheet': {
        // Sheet structure:
        //   <div.mar-sheet-backdrop[.open]>          ← e itself
        //     <div.mar-sheet-panel>
        //       <div.mar-sheet-handle></div>         ← drag affordance
        //       {sheet content children}
        //     </div>
        //   </div>
        //
        // The wrapper is always in the DOM (stable for diff). CSS
        // toggles via the `.open` class: slide-up + backdrop fade.
        // When `open=false` the wrapper is `display: none` so it
        // doesn't block clicks on the parent page.
        ensureUIStyles();
        e.className = 'mar-sheet-backdrop';
        applySheetOpenState(e, view);
        const panel = document.createElement('div');
        panel.className = 'mar-sheet-panel';
        const handle = document.createElement('div');
        handle.className = 'mar-sheet-handle';
        handle.setAttribute('aria-hidden', 'true');
        panel.appendChild(handle);
        for (const c of view.children) panel.appendChild(createDOM(c));
        e.appendChild(panel);
        attachSheetDismissDispatchers(e);
        break;
      }
      case 'confirmDialog': {
        // Modal confirmation structure:
        //   <div.mar-confirm-backdrop>                  ← e itself
        //     <div.mar-confirm-dialog>
        //       <p.mar-confirm-title>{title}</p>
        //       <div.mar-confirm-actions>
        //         <button.mar-confirm-cancel>Cancel</button>
        //         <button.mar-confirm-confirm[.destructive]>{label}</button>
        //       </div>
        //     </div>
        //   </div>
        //
        // The presence of the view in the tree IS the "is open"
        // state, when the parent's `case` returns `UI.empty`
        // instead, the entire backdrop unmounts and the modal
        // disappears with no extra wiring. Animation in / out is
        // handled by CSS keyframes on the backdrop class.
        //
        // Backdrop click + Escape both dispatch onCancel, matching
        // the iOS .confirmationDialog semantics of "tap outside =
        // cancel". The two buttons dispatch their respective msgs.
        ensureUIStyles();
        e.className = 'mar-confirm-backdrop';
        e.setAttribute('role', 'alertdialog');
        e.setAttribute('aria-modal', 'true');
        const dialog = document.createElement('div');
        dialog.className = 'mar-confirm-dialog';
        const title = document.createElement('p');
        title.className = 'mar-confirm-title';
        title.textContent = confirmDialogAttr(view, 'title');
        dialog.appendChild(title);
        const actions = document.createElement('div');
        actions.className = 'mar-confirm-actions';
        const cancelBtn = document.createElement('button');
        cancelBtn.type = 'button';
        cancelBtn.className = 'mar-confirm-cancel';
        cancelBtn.textContent = 'Cancel';
        const confirmBtn = document.createElement('button');
        confirmBtn.type = 'button';
        confirmBtn.className = 'mar-confirm-confirm';
        if (confirmDialogAttrRaw(view, 'destructive')) {
          confirmBtn.classList.add('destructive');
        }
        confirmBtn.textContent = confirmDialogAttr(view, 'confirmLabel');
        actions.appendChild(cancelBtn);
        actions.appendChild(confirmBtn);
        dialog.appendChild(actions);
        e.appendChild(dialog);
        attachConfirmDialogDispatchers(e);
        break;
      }
      case 'canvas':
        // <canvas> draw-surface. Children are Shape data, not views, so
        // we paint them imperatively instead of recursing createDOM.
        ensureUIStyles();
        setupCanvas(e, view);
        break;
      default:
        for (const c of view.children) e.appendChild(createDOM(c));
    }
    // Universal layout pass: every view honors width / height
    // (chars / lines / fill), and stacks additionally honor `align`.
    // Runs after the per-tag case so the classes compose with the
    // tag's own className assignment.
    applyLayoutAttrs(e, view);
    if (view.tag === 'hstack' || view.tag === 'vstack') applyAlignAttr(e, view);
    return e;
  }

  // ---------- Canvas (2D draw-list) renderer ----------
  //
  // A `canvas` view paints its Shape children onto a <canvas> sized to its
  // own box. Coordinates are CSS pixels and match the box the game is told
  // about via watchSize, so positions computed from the model's w/h land
  // exactly (reflow). Event listeners read el.__marView at fire time, so
  // the latest onTap / watchSize tagger is used even after a re-render.

  function canvasAttrValue(view, name) {
    const a = view && view.attrs && view.attrs.find(x => x.name === name);
    return a ? a.value : null;
  }

  // ---- safe-area insets, for the watchSize mirror ----
  //
  // env(safe-area-inset-*) exists only in CSS: there is no JS API for it. So a
  // probe element carries the four values as padding and getComputedStyle reads
  // them back in px.
  //
  // The probe is position:ABSOLUTE, and that is load-bearing. On iOS a
  // position:fixed element is itself inset to the safe area, and env() read from
  // inside one returns 0 -- the same fact that forces the UI shell's status-bar
  // strip to be position:sticky rather than fixed. A fixed probe would therefore
  // report a notch-free phone on every phone.
  //
  // .mar-canvas deliberately takes NO inset padding of its own (a game's picture
  // should reach the glass), which is exactly why the numbers have to reach the
  // app instead.
  let safeAreaProbe = null;
  function safeAreaInsets() {
    if (typeof document === 'undefined' || !document.body) return { top: 0, right: 0, bottom: 0, left: 0 };
    if (!safeAreaProbe || !safeAreaProbe.isConnected) {
      safeAreaProbe = document.createElement('div');
      safeAreaProbe.setAttribute('aria-hidden', 'true');
      safeAreaProbe.style.cssText =
        'position:absolute;top:0;left:0;width:0;height:0;visibility:hidden;pointer-events:none;' +
        'padding-top:env(safe-area-inset-top);padding-right:env(safe-area-inset-right);' +
        'padding-bottom:env(safe-area-inset-bottom);padding-left:env(safe-area-inset-left);';
      document.body.appendChild(safeAreaProbe);
    }
    const cs = getComputedStyle(safeAreaProbe);
    const px = (v) => Math.max(0, Math.round(parseFloat(v) || 0));
    return {
      top: px(cs.paddingTop), right: px(cs.paddingRight),
      bottom: px(cs.paddingBottom), left: px(cs.paddingLeft),
    };
  }

  // Every mounted canvas that is watching its box. An element is added when its
  // ResizeObserver is attached and dropped when the emit finds it detached, so
  // the set cannot pin a removed canvas alive.
  const canvasBoxWatchers = new Set();

  // Deliver { w, h, top, right, bottom, left } if anything in it changed. The
  // key dedupes: the observer and the orientation listener both call this, and
  // a rotation that only moves the notch must produce exactly one message.
  function emitCanvasBox(el) {
    if (!el.isConnected) { canvasBoxWatchers.delete(el); return; }
    const w = el.clientWidth | 0, h = el.clientHeight | 0;
    const ins = safeAreaInsets();
    const key = w + 'x' + h + ':' + ins.top + ',' + ins.right + ',' + ins.bottom + ',' + ins.left;
    if (key === el.__lastBox) return;
    el.__lastBox = key;
    const tagger = canvasAttrValue(el.__marView, 'watchSize');
    if (tagger && currentDispatch) {
      currentDispatch(apply(tagger, VRecord({
        w: VInt(w), h: VInt(h),
        top: VInt(ins.top), right: VInt(ins.right),
        bottom: VInt(ins.bottom), left: VInt(ins.left),
      }, ['w', 'h', 'top', 'right', 'bottom', 'left'])));
    }
  }

  // A rotation settles over a few frames on iOS: the event fires before the
  // layout and before env() reports the new side, so re-read a handful of times
  // across ~half a second. emitCanvasBox dedupes, so the extra reads are free.
  function rereadCanvasBoxes() {
    let n = 0;
    const tick = () => {
      canvasBoxWatchers.forEach(emitCanvasBox);
      if (++n < 6) setTimeout(tick, 90);
    };
    tick();
  }
  // Guarded on the METHOD, not just on `window`. Not every host that defines a
  // window defines a whole one: the offline audio harness in the iron-meridian
  // tools stubs a window with no event API, and an unguarded call there does not
  // degrade -- it throws at load and takes the entire runtime with it.
  if (typeof window !== 'undefined' && typeof window.addEventListener === 'function') {
    window.addEventListener('orientationchange', rereadCanvasBoxes);
    if (window.screen && window.screen.orientation &&
        typeof window.screen.orientation.addEventListener === 'function') {
      window.screen.orientation.addEventListener('change', rereadCanvasBoxes);
    }
  }

  // ---- Canvas pointer MIRROR (Canvas.watchPointers) ----
  // Per-canvas table of pressed pointers (touch contacts + mouse/pen with a
  // button down), keyed by the platform pointerId, each carrying a small stable
  // integer id (0,1,2,… smallest free, assigned on contact, reusable on
  // release) and its position in canvas CSS-pixel space. Any add / move / remove
  // schedules a single coalesced dispatch of the whole list on the next frame:
  // the Model always holds the latest complete truth. pointercancel (which the
  // browser also fires on window blur for active pointers) removes the pointer,
  // so a finger can never stick. Hovering pointers never enter the table (no
  // pointerdown); that is onHover's domain.
  // Deliver the whole pointer list. Returns false when it could NOT be
  // delivered (no tagger on this render, or no live dispatch) so the caller can
  // leave the pending flag up and retry, instead of losing the change.
  function ptrEmit(el) {
    const tagger = canvasAttrValue(el.__marView, 'watchPointers');
    if (!tagger || !currentDispatch) return false;
    const entries = Array.from((el.__ptrMap || new Map()).values()).sort((a, b) => a.id - b.id);
    const list = VList(entries.map(e => VRecord({ id: VInt(e.id), x: VInt(e.x), y: VInt(e.y) }, ['id', 'x', 'y'])));
    currentDispatch(apply(tagger, list));
    return true;
  }
  // A finger APPEARING or LEAVING is an edge, and the last event that pointer
  // will ever produce. Deferring it to a frame that might not be able to
  // deliver loses it for good: the Model keeps a finger that is no longer down,
  // which in a synth is a note that never stops. So adds and removes emit
  // synchronously, from inside the pointer handler where a live dispatch is
  // guaranteed (the same place onTap / onRelease fire from, which is why those
  // never had this bug). Only MOVES are coalesced to one message per frame,
  // because moves are the flood and the next one repairs any that is skipped.
  function ptrDispatchNow(el) {
    el.__ptrDirty = false;
    ptrEmit(el);
  }
  function ptrDispatchSoon(el) {
    if (el.__ptrDirty) return;
    el.__ptrDirty = true;
    const run = () => {
      if (!el.__ptrDirty) return;          // an edge already flushed it
      if (ptrEmit(el)) el.__ptrDirty = false;   // still stuck? keep it pending
    };
    if (typeof requestAnimationFrame !== 'undefined') requestAnimationFrame(run); else run();
  }
  function ptrAdd(el, rawId, x, y) {
    const map = el.__ptrMap || (el.__ptrMap = new Map());
    const used = new Set(); for (const v of map.values()) used.add(v.id);
    let id = 0; while (used.has(id)) id++;
    map.set(rawId, { id, x, y });
    ptrDispatchNow(el);
  }
  function ptrMove(el, rawId, x, y) {
    const map = el.__ptrMap; if (!map) return;
    const e = map.get(rawId); if (!e || (e.x === x && e.y === y)) return;
    e.x = x; e.y = y;
    ptrDispatchSoon(el);
  }
  function ptrRemove(el, rawId) {
    const map = el.__ptrMap; if (!map || !map.has(rawId)) return;
    map.delete(rawId);
    ptrDispatchNow(el);
  }

  function setupCanvas(el, view) {
    el.className = 'mar-canvas';
    if (!el.__canvasWired) {
      el.__canvasWired = true;
      // Coords of a pointer event in the canvas's CSS-pixel space (the model's
      // coord space). Shared by tap and release.
      const canvasXY = (ev) => {
        const r = el.getBoundingClientRect();
        return [Math.round(ev.clientX - r.left), Math.round(ev.clientY - r.top)];
      };
      // Tap → onTap(x, y) on pointer DOWN. For the matching "up" (onRelease) we
      // attach DOCUMENT-level pointerup/pointercancel listeners for this one
      // press, NOT setPointerCapture. Capturing the pointer has a long-debugged
      // class of iOS Safari bugs (the gesture state machine doesn't release
      // cleanly: the next tap gets eaten, and a held control can even fire a
      // spurious pointercancel mid-press, which for hold-to-move would clear the
      // press and freeze movement). Document listeners catch the release anywhere
      // on the page, the finger can slide off the control, with none of that.
      // Same pattern the list-drag handler above uses, for the same reason.
      el.addEventListener('pointerdown', (ev) => {
        const [dx, dy] = canvasXY(ev);
        const tapTagger = canvasAttrValue(el.__marView, 'onTap');
        if (tapTagger && currentDispatch) {
          currentDispatch(apply(apply(tapTagger, VInt(dx)), VInt(dy)));
        }
        // Record this contact in the pointer mirror (watchPointers).
        ptrAdd(el, ev.pointerId, dx, dy);
        // One-shot release + move bound to THIS pointer: update the mirror, fire
        // onDrag on each pointermove and onRelease on the matching
        // pointerup/cancel, then detach. Nothing outlives the press. Per-pointer
        // listeners mean each finger is tracked independently (multi-touch), and
        // document scope lets a finger slide off the canvas and still be followed
        // (the canvas element itself has no move event once the finger leaves).
        const downId = ev.pointerId;
        const onMove = (mv) => {
          if (mv.pointerId !== downId) return;
          const [x, y] = canvasXY(mv);
          ptrMove(el, downId, x, y);
          const dragTagger = canvasAttrValue(el.__marView, 'onDrag');
          if (dragTagger && currentDispatch) {
            currentDispatch(apply(apply(dragTagger, VInt(x)), VInt(y)));
          }
        };
        const onUp = (upEv) => {
          if (upEv.pointerId !== downId) return;
          document.removeEventListener('pointerup', onUp);
          document.removeEventListener('pointercancel', onUp);
          document.removeEventListener('pointermove', onMove);
          ptrRemove(el, downId);
          const relTagger = canvasAttrValue(el.__marView, 'onRelease');
          if (relTagger && currentDispatch) {
            const [x, y] = canvasXY(upEv);
            currentDispatch(apply(apply(relTagger, VInt(x)), VInt(y)));
          }
        };
        document.addEventListener('pointermove', onMove);
        document.addEventListener('pointerup', onUp);
        document.addEventListener('pointercancel', onUp);
      });
      // iOS Safari: the CSS armor above (user-select / touch-callout none, on
      // the element AND on html.mar-canvas-page) is still not enough: holding
      // a finger down pops the selection LOUPE, that grey magnifying blob, over
      // the surface. Reported on the synth, where holding a key to sustain a
      // note IS a long press. Safari decides to begin that gesture from
      // touchstart, so the only thing that reliably calls it off is preventing
      // touchstart's default. Guarded on the page-armor class so a canvas
      // EMBEDDED in a normal page (a showcase strip) keeps its native scroll:
      // only a canvas that owns the whole page swallows the gesture.
      el.addEventListener('touchstart', (ev) => {
        if (document.documentElement.classList.contains('mar-canvas-page')) ev.preventDefault();
      }, { passive: false });   // preventDefault is ignored on a passive listener
      // Desktop input trio (opt-in, inert on touch). All read el.__marView at
      // fire time so the latest tagger wins after a re-render.
      //   onHover  - pointer move with NO button held (a pressed move is
      //              onDrag's job). Touch has no button-less move, so this
      //              never fires on touch, exactly as documented.
      el.addEventListener('pointermove', (ev) => {
        if (ev.buttons !== 0) return;
        const tagger = canvasAttrValue(el.__marView, 'onHover');
        if (tagger && currentDispatch) {
          const [x, y] = canvasXY(ev);
          currentDispatch(apply(apply(tagger, VInt(x)), VInt(y)));
        }
      });
      //   onAltTap: right-click / two-finger tap. contextmenu is the portable
      //              signal; preventDefault swallows the native menu.
      el.addEventListener('contextmenu', (ev) => {
        const tagger = canvasAttrValue(el.__marView, 'onAltTap');
        if (!tagger) return;
        ev.preventDefault();
        if (currentDispatch) {
          const [x, y] = canvasXY(ev);
          currentDispatch(apply(apply(tagger, VInt(x)), VInt(y)));
        }
      });
      //   onWheel  - scroll delta as (dx, dy). preventDefault stops the page
      //              from scrolling; both signs are stable across pixel/line
      //              devices. dx carries horizontal (trackpad) scroll; callers
      //              that only want vertical just ignore it. Non-passive so
      //              preventDefault works.
      el.addEventListener('wheel', (ev) => {
        const tagger = canvasAttrValue(el.__marView, 'onWheel');
        if (!tagger) return;
        ev.preventDefault();
        if (currentDispatch) {
          currentDispatch(apply(apply(tagger, VInt(Math.round(ev.deltaX))), VInt(Math.round(ev.deltaY))));
        }
      }, { passive: false });
      // Resize → redraw + watchSize({ w, h, top, right, bottom, left }).
      // ResizeObserver fires once on observe, seeding the real box into the
      // model immediately: the size mirror's "seed on subscribe" contract.
      //
      // The insets ride along because they usually change at the same moment
      // the box does -- turning a phone upright does both -- and the observer
      // already fires then, since .mar-canvas is 100%/100dvh.
      //
      // The exception is the whole reason the insets exist: rotating a phone
      // 180 degrees, landscape-left to landscape-right, leaves the box byte for
      // byte identical and moves the notch from one side to the other. No
      // ResizeObserver fires, so left/right would stay swapped -- wrong on
      // exactly one device held exactly one way, which is the failure ADR 0034
      // exists to prevent. canvasBoxWatchers lets an orientation change re-emit
      // the same box with the new insets.
      if (typeof ResizeObserver !== 'undefined') {
        el.__canvasRO = new ResizeObserver(() => { drawCanvas(el); emitCanvasBox(el); });
        el.__canvasRO.observe(el);
        canvasBoxWatchers.add(el);
      }
    }
    drawCanvas(el);
  }

  function drawCanvas(el) {
    const view = el.__marView;
    const ctx = el.getContext && el.getContext('2d');
    if (!view || !ctx) return;
    // The canvas render mode (mandatory `CanvasMode` arg) decides the backing
    // resolution: see docs/adrs/0001-canvas-render-resolution.md.
    //
    // Pixelated: buffer = 1 CSS px, nearest-neighbour upscale. A full-screen
    // action game repaints every pixel each frame; at retina dpr (2–3 on
    // phones) that's 4–9× the fill and mobile Safari drops below 60 fps. Since
    // the frame loop ticks once per painted frame (subSources.timeEvery), a
    // slower paint doesn't just look janky: the world runs in slow motion. At
    // 1× an iPhone paints ~1/3 the pixels, so it holds 60 fps; nearest-neighbour
    // keeps the upscale crisp instead of bilinear-blurring it.
    //
    // Crisp: buffer = devicePixelRatio, drawn 1:1 to the panel (smooth). Text
    // and shapes are sharp on retina. Costs 4–9× the fill, so it's for
    // turn-based / text-heavy canvases that don't repaint hot (a card game),
    // NOT the 60 fps action games. Either way the drawing space stays CSS-px
    // (the transform absorbs dpr), so onTap/onDrag coordinates are unchanged.
    const modeV = canvasAttrValue(view, 'canvasMode');
    const crisp = !!(modeV && modeV.s === 'crisp');
    const dpr = crisp ? (window.devicePixelRatio || 1) : 1;
    const boxW = el.clientWidth || 0;
    const boxH = el.clientHeight || 0;
    const bufW = Math.max(1, Math.round(boxW * dpr));
    const bufH = Math.max(1, Math.round(boxH * dpr));
    if (el.width !== bufW) el.width = bufW;
    if (el.height !== bufH) el.height = bufH;
    const wantRender = crisp ? 'auto' : 'pixelated';
    if (el.style.imageRendering !== wantRender) el.style.imageRendering = wantRender;
    // 1 drawing unit = 1 CSS px = dpr buffer px (identity when dpr === 1).
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    ctx.clearRect(0, 0, boxW, boxH);
    drawShapes(ctx, view.children || []);
  }

  function drawShapes(ctx, shapes) {
    for (const s of shapes) drawShape(ctx, s);
  }

  function canvasNum(v) { return v && v.k === 'I' ? v.n : 0; }
  // An Angle carries deci-degrees (0..3599). Radians are what the 2D
  // context wants, so the conversion divides by 1800 instead of 180.
  function canvasRadians(v) { return v && v.k === 'A' ? v.deci * Math.PI / 1800 : 0; }
  function canvasStr(v) { return v && v.k === 'S' ? v.s : ''; }

  function canvasColor(c) {
    // rgb : VCtor('rgb', [r, g, b]) · rgba : VCtor('rgba', [r, g, b, a]) with
    // a as a percent int (Mar has no floats). Anything else → transparent.
    if (!c || c.k !== 'C' || !c.args) return 'transparent';
    const r = canvasNum(c.args[0]) | 0, g = canvasNum(c.args[1]) | 0, b = canvasNum(c.args[2]) | 0;
    if (c.tag === 'rgb') return 'rgb(' + r + ',' + g + ',' + b + ')';
    if (c.tag === 'rgba') {
      const a = Math.max(0, Math.min(100, canvasNum(c.args[3]) | 0));
      return 'rgba(' + r + ',' + g + ',' + b + ',' + (a / 100) + ')';
    }
    return 'transparent';
  }

  function canvasTextAlign(a) {
    if (a && a.k === 'C') {
      if (a.tag === 'Left') return 'left';
      if (a.tag === 'Right') return 'right';
    }
    return 'center';
  }

  function applyCanvasTransform(ctx, t) {
    if (!t || t.k !== 'C') return;
    switch (t.tag) {
      case 'Translate': ctx.translate(canvasNum(t.args[0]), canvasNum(t.args[1])); break;
      // Scale args are percent ints (100 = 1×); Rotate takes an Angle.
      case 'Scale':     ctx.scale(canvasNum(t.args[0]) / 100, canvasNum(t.args[1]) / 100); break;
      case 'Rotate':    ctx.rotate(canvasRadians(t.args[0])); break;
      // Alpha / Blend are not matrix ops: groupAlpha and groupBlend pull
      // them out before this runs.
    }
  }

  // Canvas.Blend on a group as a globalCompositeOperation, or null when
  // absent (which means Normal: exactly what a group did before Blend
  // existed, so untouched code keeps drawing the same bytes). Unlike Alpha,
  // repeats do NOT combine: opacities compose, modes don't, so the last one
  // in the list simply wins.
  const BLEND_OPS = {
    Normal: 'source-over',
    Add: 'lighter',
    Multiply: 'multiply',
    Screen: 'screen',
    Erase: 'destination-out',
  };
  function groupBlend(transforms) {
    let op = null;
    for (const t of transforms) {
      if (t && t.k === 'C' && t.tag === 'Blend') {
        const mode = t.args[0];
        if (mode && mode.k === 'C' && BLEND_OPS[mode.tag]) op = BLEND_OPS[mode.tag];
      }
    }
    return op;
  }

  // Canvas.Alpha, if present on a group, as a 0..1 fraction; null otherwise.
  // Repeats multiply, so `[Alpha 50, Alpha 50]` is 25%, matching how nesting
  // two alpha groups behaves.
  function groupAlpha(transforms) {
    let a = null;
    for (const t of transforms) {
      if (t && t.k === 'C' && t.tag === 'Alpha') {
        const pct = Math.max(0, Math.min(100, canvasNum(t.args[0]) | 0));
        a = (a === null ? 1 : a) * (pct / 100);
      }
    }
    return a;
  }

  // Scratch buffers for layered groups, one per nesting depth so a layered
  // group inside a layered group doesn't scribble on its parent's buffer.
  // They live between frames (allocating a canvas per frame would be the whole
  // cost of the feature) and are resized to follow the target.
  const scratchLayers = [];
  function scratchLayer(depth, w, h) {
    let c = scratchLayers[depth];
    if (!c) { c = document.createElement('canvas'); scratchLayers[depth] = c; }
    if (c.width !== w) c.width = w;
    if (c.height !== h) c.height = h;
    return c;
  }
  let layerDepth = 0;

  // Draw the group into its own buffer, then stamp that buffer ONCE.
  //
  // This is the whole point of Alpha over `rgba`: fading each shape on its own
  // would let the group's parts show through each other, because every shape
  // would blend against the ones already painted under it. Compositing first
  // and fading once means overlaps inside the group never blend at all: a
  // sprite fades as one object, a cloud of overlapping puffs comes out evenly
  // translucent instead of showing its seams.
  //
  // `op` (from Canvas.Blend) applies BOTH to the children as they land in the
  // scratch and to the stamp, so shapes inside an Add group still sum with
  // each other AND the finished group still sums onto the scene. That agrees
  // with the no-layer path because Add / Multiply / Screen are commutative per
  // channel: folding the shapes together first lands where folding them into
  // the backdrop one by one would.
  //
  // Erase is the exception: its children paint NORMALLY into the scratch,
  // building one silhouette, and only the stamp cuts. That is what makes
  // `Alpha 50 + Erase` mean "a hole at half strength": letting the children
  // erase each other inside the scratch would erase nothing at all (the
  // scratch starts empty), and erasing them against the target instead would
  // bite deeper wherever two erasers overlap.
  function drawLayerGroup(ctx, transforms, kids, alpha, op) {
    const target = ctx.canvas;
    const layer = scratchLayer(layerDepth, target.width, target.height);
    const lctx = layer.getContext('2d');
    // Same device transform as the target, so the group lands on the same
    // pixels it would have hit drawing straight through.
    const m = ctx.getTransform();
    lctx.setTransform(1, 0, 0, 1, 0, 0);
    lctx.globalCompositeOperation = 'source-over';
    lctx.clearRect(0, 0, layer.width, layer.height);
    lctx.setTransform(m);
    if (op && op !== 'destination-out') lctx.globalCompositeOperation = op;
    for (const t of transforms) applyCanvasTransform(lctx, t);
    layerDepth++;
    drawShapes(lctx, kids);
    layerDepth--;
    ctx.save();
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    if (op) ctx.globalCompositeOperation = op;
    ctx.globalAlpha = alpha;
    ctx.drawImage(layer, 0, 0);
    ctx.restore();
  }

  function drawShape(ctx, s) {
    if (!s || s.k !== 'C') return;
    switch (s.tag) {
      case 'rect':
        ctx.fillStyle = canvasColor(s.args[4]);
        ctx.fillRect(canvasNum(s.args[0]), canvasNum(s.args[1]), canvasNum(s.args[2]), canvasNum(s.args[3]));
        break;
      case 'circle':
        ctx.fillStyle = canvasColor(s.args[3]);
        ctx.beginPath();
        ctx.arc(canvasNum(s.args[0]), canvasNum(s.args[1]), Math.max(0, canvasNum(s.args[2])), 0, Math.PI * 2);
        ctx.fill();
        break;
      case 'triangle':
        ctx.fillStyle = canvasColor(s.args[6]);
        ctx.beginPath();
        ctx.moveTo(canvasNum(s.args[0]), canvasNum(s.args[1]));
        ctx.lineTo(canvasNum(s.args[2]), canvasNum(s.args[3]));
        ctx.lineTo(canvasNum(s.args[4]), canvasNum(s.args[5]));
        ctx.closePath();
        ctx.fill();
        break;
      case 'text': {
        const size = canvasNum(s.args[2]);
        ctx.font = '600 ' + size + 'px -apple-system, system-ui, "Segoe UI", Roboto, sans-serif';
        ctx.textAlign = canvasTextAlign(s.args[3]);
        ctx.textBaseline = 'middle';
        ctx.fillStyle = canvasColor(s.args[4]);
        ctx.fillText(canvasStr(s.args[5]), canvasNum(s.args[0]), canvasNum(s.args[1]));
        break;
      }
      case 'group': {
        const transforms = (s.args[0] && s.args[0].k === 'L' && s.args[0].xs) ? s.args[0].xs : [];
        const kids = (s.args[1] && s.args[1].k === 'L' && s.args[1].xs) ? s.args[1].xs : [];
        const alpha = groupAlpha(transforms);
        const op = groupBlend(transforms);
        // No Alpha means no buffer, for every mode: one context-state
        // assignment and the children draw straight through. Erase included:
        // erasing shape by shape leaves dst × Π(1-aᵢ), which is exactly what
        // stamping their composited silhouette once would leave. (Measured
        // both ways on a pixel bench, 2026-07-15: byte-identical.)
        if (alpha === null) {
          ctx.save();
          if (op) ctx.globalCompositeOperation = op;
          for (const t of transforms) applyCanvasTransform(ctx, t);
          drawShapes(ctx, kids);
          ctx.restore();
        } else if (alpha > 0) {
          drawLayerGroup(ctx, transforms, kids, alpha, op);
        }
        break;
      }
    }
  }

  // ---------- Sheet dispatch + history glue ----------
  //
  // A sheet's open/close state is owned by the parent's Model. To make
  // the browser back button close the sheet (matching iOS swipe-down),
  // the framework injects a synthetic history entry when the sheet
  // opens, and pops it when the sheet closes. The `outlet` field on
  // the view is the identifier used to avoid clobbering legitimate
  // history navigation.
  //
  // Tracking which sheets are currently open by outlet name; lets us
  // tell "back button popped our entry" apart from "the user clicked
  // a link". A push/pop pair is balanced when the parent flips
  // open=true→false on its own (e.g. after a successful create).
  const sheetHistory = { open: new Set() };

  function applySheetOpenState(node, view) {
    const open = sheetIsOpen(view);
    // Single class for the open state. Default (no class) = closed.
    // The .mar-sheet-closed class was redundant once display: none was
    // removed; transitions live on the base + .mar-sheet-open selectors.
    node.classList.toggle('mar-sheet-open', open);
    const outlet = sheetOutlet(view);
    syncSheetHistory(outlet, open);
  }

  // Read attrs directly off the view: getAttr() in this file
  // assumes string attrs (returns `.value.s`), which would silently
  // drop a VBool. Mirroring toggleIsOn's direct-lookup pattern.
  function sheetIsOpen(view) {
    const a = view.attrs && view.attrs.find(a => a.name === 'open');
    return !!(a && a.value && a.value.b);
  }
  function sheetOutlet(view) {
    const a = view.attrs && view.attrs.find(a => a.name === 'outlet');
    return a && a.value && typeof a.value.s === 'string' ? a.value.s : '';
  }

  // ---------- Confirm dialog helpers ----------
  //
  // Attrs come off the view typed (VString/VBool/Msg-shaped), unlike
  // the rest of the codebase that uses getAttr() and silently
  // assumes a string. Each helper reads its expected type and
  // returns a usable JS value with a safe default if anything's
  // missing.

  function confirmDialogAttr(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a && a.value && typeof a.value.s === 'string' ? a.value.s : '';
  }
  function confirmDialogAttrRaw(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a && a.value ? (a.value.b === true) : false;
  }
  function confirmDialogHandler(view, name) {
    const a = view.attrs && view.attrs.find(a => a.name === name);
    return a && a.value ? a.value : null;
  }

  // attachConfirmDialogDispatchers wires the three dispatch sources:
  //   - Click on the destructive button → onConfirm
  //   - Click on the cancel button     → onCancel
  //   - Click on the backdrop (not on the dialog itself) → onCancel
  //
  // We bind to the wrapper element (which IS the backdrop) and use
  // event delegation so the listeners survive child re-renders.
  // The escape-key handler is bound to document so it works
  // regardless of focus, but only fires while a confirm dialog is
  // mounted (we check via :scope traversal on each press).
  function attachConfirmDialogDispatchers(backdropEl) {
    if (backdropEl.__marConfirmWired) return;
    backdropEl.__marConfirmWired = true;

    backdropEl.addEventListener('click', (ev) => {
      const v = backdropEl.__marView;
      if (!v || !currentDispatch) return;
      // Find which logical button (if any) was hit.
      let kind = null;
      if (ev.target.classList.contains('mar-confirm-confirm')) {
        kind = 'onConfirm';
      } else if (ev.target.classList.contains('mar-confirm-cancel')) {
        kind = 'onCancel';
      } else if (ev.target === backdropEl) {
        // Bare-backdrop click (didn't hit the dialog): treat as cancel.
        kind = 'onCancel';
      }
      if (!kind) return;
      const handler = confirmDialogHandler(v, kind);
      if (handler) currentDispatch(handler);
    });
  }

  // Document-level Escape handler: installed once. Walks the
  // currently-mounted DOM for a `.mar-confirm-backdrop` and
  // dispatches its onCancel. Doing this at the document level (vs.
  // on each backdrop) means we don't need focus inside the dialog
  // for Escape to work, which matches iOS / Apple Music behavior
  // (Escape always closes, regardless of where the focus ring
  // happens to be).
  if (typeof document !== 'undefined' && !document.__marConfirmEscWired) {
    document.__marConfirmEscWired = true;
    document.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Escape') return;
      const backdrop = document.querySelector('.mar-confirm-backdrop');
      if (!backdrop) return;
      const v = backdrop.__marView;
      if (!v || !currentDispatch) return;
      const handler = confirmDialogHandler(v, 'onCancel');
      if (handler) currentDispatch(handler);
    });
  }

  // syncSheetHistory mirrors the sheet's open state into the browser
  // history. push when open transitions false→true, back when
  // true→false (so the user-initiated dismiss leaves no orphan entry).
  // popstate handler in attachSheetDismissDispatchers fires the
  // user-back-button path separately.
  function syncSheetHistory(outlet, open) {
    if (!outlet) return;
    const wasOpen = sheetHistory.open.has(outlet);
    if (open && !wasOpen) {
      sheetHistory.open.add(outlet);
      const url = new URL(location.href);
      url.searchParams.set('sheet', outlet);
      // Carry the covered page's nav bookkeeping onto the synthetic sheet
      // entry so it reads as the SAME depth as the page it sits over (a
      // sheet is an overlay, not a deeper screen). Without this the entry
      // zeroes out currentNavDepth(), and a navigation launched from an
      // open sheet would mis-count the destination's depth.
      const cur = history.state || {};
      history.pushState(
        { __marSheet: outlet
        , marDepth: (typeof cur.marDepth === 'number' ? cur.marDepth : 0)
        , marEntry: cur.marEntry
        , prevTitle: cur.prevTitle || ''
        },
        '',
        url.toString());
    } else if (!open && wasOpen) {
      sheetHistory.open.delete(outlet);
      // Remove our pushed entry with history.back(), but DEFER it to a
      // microtask. A single dispatch can both close a sheet AND navigate
      // ("create the record, then jump to it"): the effect (Nav.push) runs
      // right after this render (see currentDispatch). When it does, the
      // nav ADOPTS our synthetic entry: pushNav overwrites it and clears
      // sheetPendingBack, so popping here is cancelled; without that, the
      // deferred back() would fire after the nav's pushState and bounce us
      // straight off the page we just opened. With no nav in the dispatch,
      // the microtask still runs and cleans the entry exactly as before.
      sheetPendingBack = outlet;
      const runBack = () => {
        if (sheetPendingBack !== outlet) return;            // a nav adopted it
        sheetPendingBack = null;
        // Second guard: only pop if OUR entry is still the current one
        // (a nav may have replaceState'd over it).
        if (history.state && history.state.__marSheet === outlet) {
          sheetClosingProgrammatically = true;
          history.back();
        }
      };
      if (typeof queueMicrotask !== 'undefined') queueMicrotask(runBack);
      else setTimeout(runBack, 0);
    }
  }

  let sheetClosingProgrammatically = false;
  // Outlet whose synthetic history entry is queued for cleanup but not yet
  // popped. A navigation in the same dispatch clears this to adopt the
  // entry instead of letting the deferred history.back() bounce the nav.
  let sheetPendingBack = null;

  // attachSheetDismissDispatchers binds the backdrop click, Escape
  // key, and browser back button to the sheet's onDismiss Msg. Called
  // once per sheet wrapper element; reads node.__marView at event
  // time so the latest onDismiss is dispatched even after re-render.
  function attachSheetDismissDispatchers(node) {
    if (node.__sheetDispatchersBound) return;
    node.__sheetDispatchersBound = true;

    // Backdrop click: only dismiss when the user clicks the wrapper
    // itself, not bubbled from the panel content.
    node.addEventListener('click', (ev) => {
      if (ev.target !== node) return;
      dispatchSheetDismiss(node);
    });

    // Escape key: only when this sheet is open (sheets stack
    // semantically; outermost sheet closes first).
    document.addEventListener('keydown', (ev) => {
      if (ev.key !== 'Escape') return;
      if (!node.__marView || !sheetIsOpen(node.__marView)) return;
      dispatchSheetDismiss(node);
    });

    // Browser back button: popstate fires when the user hits Back.
    // If WE just called history.back() to clean up an outlet, ignore
    // (the parent already closed the sheet via Msg). Otherwise the
    // user wants to close: dispatch onDismiss so the parent flips
    // its open flag.
    window.addEventListener('popstate', () => {
      if (sheetClosingProgrammatically) {
        sheetClosingProgrammatically = false;
        return;
      }
      if (!node.__marView || !sheetIsOpen(node.__marView)) return;
      // Forget the outlet: the URL entry is already gone.
      const outlet = sheetOutlet(node.__marView);
      if (outlet) sheetHistory.open.delete(outlet);
      dispatchSheetDismiss(node);
    });
  }

  function dispatchSheetDismiss(node) {
    const v = node.__marView;
    if (!currentDispatch || !v || v.msg == null) return;
    currentDispatch(v.msg);
  }

  // patchDOM updates `node` (currently rendering oldView, available as
  // node.__marView) so it matches newView, mutating in place where possible
  // and replacing only when the tag changes. Listeners stay attached
  // because they read view.msg from node.__marView.
  function patchDOM(node, newView) {
    const oldView = node.__marView;
    if (!oldView || oldView.tag !== newView.tag) {
      const replacement = createDOM(newView);
      node.parentNode.replaceChild(replacement, node);
      return replacement;
    }
    setMarView(node, newView);
    // Universal layout pass (mirror of createDOM's tail): re-sync
    // the width / height classes + styles and the stack `align`
    // class on every patch, so dynamically swapped sizing attrs
    // (`if compact then width (chars 12) else width fill`) track the
    // model. Idempotent and cheap (classList.toggle + style writes).
    applyLayoutAttrs(node, newView);
    if (newView.tag === 'hstack' || newView.tag === 'vstack') applyAlignAttr(node, newView);

    switch (newView.tag) {
      case 'canvas':
        // Re-issue the draw list: the shape children change every frame.
        // The listeners + ResizeObserver were wired once in createDOM and
        // read node.__marView (freshly set above) at fire time.
        drawCanvas(node);
        break;
      case 'text':
      case 'title':
      case 'subtitle':
      case 'errorText':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        break;
      case 'span': {
        // Inline run inside a paragraph. Text AND styling derive from the
        // view, so re-sync both (a dynamic span used to freeze at its first
        // value). If the link-ness flipped, the DOM element type changed
        // (span <-> a), which a class swap can't fix: replace instead.
        const wantsLink = (newView.attrs || []).some(a => a.name === 'inlineLink');
        if (wantsLink !== (node.tagName === 'A')) {
          const replacement = createDOM(newView);
          node.parentNode.replaceChild(replacement, node);
          return replacement;
        }
        styleInlineSpan(node, newView);
        break;
      }
      case 'image': {
        const newSrc = getAttr(newView, 'src');
        const newAlt = getAttr(newView, 'alt');
        if (node.getAttribute('src') !== newSrc) node.setAttribute('src', newSrc);
        if (node.getAttribute('alt') !== newAlt) node.setAttribute('alt', newAlt);
        // Clear inline sizing/object-fit before re-applying so a model
        // change that drops the size/contentMode attr doesn't leave a
        // stale style behind.
        node.style.width = ''; node.style.height = '';
        node.style.maxWidth = ''; node.style.objectFit = '';
        applyImageAttrs(node, newView);
        break;
      }
      case 'button':
        if (node.textContent !== newView.text) node.textContent = newView.text;
        applyDisabledAttr(node, newView);
        break;
      case 'navigationLink': {
        const newHref = getAttr(newView, 'href');
        if (node.getAttribute('href') !== newHref) node.setAttribute('href', newHref);
        applyAnchorDisabled(node, newView);
        // Patch children in place: same shape as the other
        // container tags (hstack/vstack/section). Lets a focused
        // input nested inside a navigationLink survive a re-render.
        patchChildrenPositional(node, oldView.children || [], newView.children || [], 'navigationLink');
        break;
      }
      case 'textField':
      case 'textArea':
        // Only write if the value diverges: avoids resetting the cursor
        // mid-keystroke when the model just echoes what the user typed.
        // Same patch path for both shapes; the input vs textarea is
        // already pinned by node.tagName from the initial createDOM.
        if (node.value !== newView.text) node.value = newView.text;
        applyDisabledAttr(node, newView);
        // Re-apply sizing on every patch, cheap (idempotent: same
        // attrs produce the same inline style), and necessary when
        // user code dynamically swaps the width/height attr (rare
        // but valid: e.g. `if model.compact then width (chars 12)
        // else width (chars 40)`).
        applySizing(node, newView);
        break;
      case 'datePicker': {
        // The value is the Date attr's YYYY-MM-DD (dateInputValue).
        // Written only when it diverges and the field is not being
        // typed in, so a re-render does not undo a year half typed;
        // dateInputLeft brings the field back to the model on blur.
        const next = dateInputValue(newView);
        if (node.value !== next && !hasFocus(node)) node.value = next;
        applyDisabledAttr(node, newView);
        applySizing(node, newView);
        break;
      }
      case 'picker': {
        // Re-render the option list only when something changed.
        // Comparing the underlying VList / VFn / VValue refs lets
        // the common "same options, only selected differs" path
        // avoid recreating the <option> nodes (which would flicker
        // the dropdown if open). When refs match, just sync the
        // select.value index.
        const select = node.querySelector(':scope > .mar-picker-select');
        if (!select) break;
        applyDisabledAttr(select, newView);
        node.classList.toggle('mar-disabled', isDisabled(newView));
        applySizing(node, newView);
        const oldOptions = oldView.attrs.find(a => a.name === 'options');
        const newOptions = newView.attrs.find(a => a.name === 'options');
        const oldToLabel = oldView.attrs.find(a => a.name === 'toLabel');
        const newToLabel = newView.attrs.find(a => a.name === 'toLabel');
        const optionsChanged =
          !oldOptions || !newOptions ||
          oldOptions.value !== newOptions.value ||
          (oldToLabel && newToLabel && oldToLabel.value !== newToLabel.value);
        if (optionsChanged) {
          renderPickerOptions(select, newView);
        } else {
          const selectedAttr = newView.attrs.find(a => a.name === 'selected');
          const opts = newOptions.value.xs;
          if (selectedAttr && selectedAttr.value) {
            for (let i = 0; i < opts.length; i++) {
              if (eqValues(opts[i], selectedAttr.value)) {
                if (select.value !== String(i)) select.value = String(i);
                break;
              }
            }
          }
        }
        break;
      }
      case 'empty':
        break;
      case 'spacer':
        // No content, no listeners: nothing to update. The CSS
        // class drives the layout entirely.
        break;
      case 'toggle': {
        // Slot 0: <span.mar-toggle-label>, sync text.
        // Slot 1: <input.mar-toggle-switch>, sync checked.
        // Listener on the input reads view.msg from the label via
        // closure, so __marView already updated above is enough.
        const labelSpan = node.querySelector(':scope > .mar-toggle-label');
        const sw = node.querySelector(':scope > .mar-toggle-switch');
        if (labelSpan && labelSpan.textContent !== newView.text) {
          labelSpan.textContent = newView.text;
        }
        if (sw) {
          const next = toggleIsOn(newView);
          if (sw.checked !== next) sw.checked = next;
          applyDisabledAttr(sw, newView);
        }
        node.classList.toggle('mar-disabled', isDisabled(newView));
        break;
      }
      case 'sheet': {
        // Apply new open-state (drives CSS class + history pushState/back
        // sync). Then diff the panel's content children.
        applySheetOpenState(node, newView);
        const panel = node.querySelector(':scope > .mar-sheet-panel');
        if (panel) {
          // Panel's first child is the persistent .mar-sheet-handle:
          // diff starts from index 1.
          const oldChildren = oldView.children || [];
          const newChildren = newView.children || [];
          // Children render at panel[1..], handle is at panel[0].
          const offset = 1;
          for (let i = 0; i < newChildren.length; i++) {
            const child = panel.children[offset + i];
            if (!child) {
              panel.appendChild(createDOM(newChildren[i]));
            } else if (child.__marView && child.__marView.tag === newChildren[i].tag) {
              patchDOM(child, newChildren[i]);
            } else {
              panel.replaceChild(createDOM(newChildren[i]), child);
            }
          }
          // Remove extras.
          while (panel.children.length > offset + newChildren.length) {
            panel.removeChild(panel.lastChild);
          }
        }
        break;
      }
      case 'confirmDialog': {
        // Refresh the title + button label + destructive class. The
        // attrs are stored on the view; we just rewrite the
        // corresponding DOM nodes in place. Listeners stay attached
        // via attachConfirmDialogDispatchers (it's idempotent and
        // bound to the backdrop wrapper, not the inner buttons).
        const titleEl = node.querySelector(':scope > .mar-confirm-dialog > .mar-confirm-title');
        if (titleEl) titleEl.textContent = confirmDialogAttr(newView, 'title');
        const confirmBtn = node.querySelector(':scope > .mar-confirm-dialog > .mar-confirm-actions > .mar-confirm-confirm');
        if (confirmBtn) {
          confirmBtn.textContent = confirmDialogAttr(newView, 'confirmLabel');
          confirmBtn.classList.toggle('destructive', confirmDialogAttrRaw(newView, 'destructive'));
        }
        // No children to diff: the dialog's content is fully
        // attribute-driven. The view's onConfirm / onCancel
        // handlers live on the (newly-stored) view via setMarView;
        // the listener reads them from there at click-time, so they
        // automatically pick up the new dispatchers.
        break;
      }
      // navigationStack / uiSection patch in place: the surrounding
      // chrome (nav bar, section header/footer) gets re-rendered, but
      // the body wrapper stays the same DOM node so any focused input
      // inside keeps focus + cursor + selection.
      case 'navigationStack': {
        // Slots 0+1: pinned toolbar row + large-title header. Build the
        // fresh chrome, carry the scrolled state onto it first (so a
        // scrolled page doesn't read as "changed" and the inline title
        // doesn't blink off for a frame on every patch), then decide
        // whether to actually swap the DOM.
        const oldRow = node.children[0];
        const oldBar = node.children[1];
        const [newRow, newBar] = buildNavigationChrome(newView);
        if (oldRow && oldRow.classList.contains('mar-nav-scrolled')) {
          newRow.classList.add('mar-nav-scrolled');
        }
        // Only replace the chrome nodes when the bar's rendered output
        // actually changed. Replacing them mid-click is the bug this
        // guards: a real mousedown → (re-render) → mouseup straddles two
        // different button nodes, so the browser fires no `click` and the
        // back button / toolbar actions go DEAD on any page that animates
        // via Time.every (the whole view re-renders every frame, but the
        // bar's content is identical). Keeping the mounted nodes preserves
        // their identity, and their observer + listeners, so the click
        // lands. The observer only toggles mar-nav-scrolled (carried
        // above), so outerHTML is a faithful "did the bar change?" check.
        const chromeSame = oldRow && oldBar
          && oldRow.outerHTML === newRow.outerHTML
          && oldBar.outerHTML === newBar.outerHTML;
        if (chromeSame) {
          // Bin the throwaway row's observer so it doesn't leak watching a
          // detached header; keep the mounted row (and its live observer).
          if (newRow._marNavObserver) newRow._marNavObserver.disconnect();
        } else {
          if (oldRow && oldRow._marNavObserver) oldRow._marNavObserver.disconnect();
          node.replaceChild(newRow, node.children[0]);
          node.replaceChild(newBar, node.children[1]);
        }
        // Slot 2: body wrapper. Diff its children, that's where
        // form/list/etc live, possibly with a focused input below.
        const body = node.querySelector(':scope > .mar-nav-body');
        if (body) {
          patchChildrenPositional(body, oldView.children, newView.children, 'navigationStack');
        }
        break;
      }
      case 'uiSection':
      case 'uiKeyedList': {
        const headerText = getAttr(newView, 'header');
        const footerText = getAttr(newView, 'footer');
        const h = node.querySelector(':scope > .mar-section-header');
        const body = node.querySelector(':scope > .mar-section-body');
        const f = node.querySelector(':scope > .mar-section-footer');
        if (h) {
          if (h.textContent !== headerText) h.textContent = headerText;
          h.style.display = headerText ? '' : 'none';
        }
        if (f) {
          if (f.textContent !== footerText) f.textContent = footerText;
          f.style.display = footerText ? '' : 'none';
        }
        if (body) {
          // When the editing flag of `onMove` OR `onDelete` flips,
          // the row structure changes (drag handles + delete
          // buttons appear / disappear; listeners get rebound).
          // Rebuild the section body wholesale: toggling edit
          // mode is rare (one tap) and a fresh subtree is simpler
          // than diffing every affordance in/out.
          //
          // We only watch the `.editing` flag on onDelete, not the
          // presence of the attr itself: the delete button is gated
          // entirely on editing=true (see attachRowDeleteAffordance
          //: browse mode shows no destructive control), so the
          // attr can come and go between renders without visual
          // change as long as editing stays false.
          const oldOnMoveS = getAttrRaw(oldView, 'onMove');
          const newOnMoveS = getAttrRaw(newView, 'onMove');
          const oldEdS = !!(oldOnMoveS && oldOnMoveS.editing);
          const newEdS = !!(newOnMoveS && newOnMoveS.editing);
          const oldOnDelS = getAttrRaw(oldView, 'onDelete');
          const newOnDelS = getAttrRaw(newView, 'onDelete');
          const oldDelEdS = !!(oldOnDelS && oldOnDelS.editing);
          const newDelEdS = !!(newOnDelS && newOnDelS.editing);
          if (oldEdS !== newEdS || oldDelEdS !== newDelEdS) {
            const fresh = createDOM(newView);
            node.parentNode.replaceChild(fresh, node);
            break;
          }
          // Stable editing state: keyed reconciliation handles
          // reorders without losing handle wiring (drag listeners
          // are bound to the section-body element, not the rows).
          patchChildrenPositional(body, oldView.children, newView.children, 'uiSection');
          // Defensive sweep: ensure every current row has its
          // edit-mode affordances. patchChildrenKeyed can create
          // new rows via createDOM(rowView) when a new key appears
          // (Add Task, restored row via Undo, tag swap toggle ↔
          // hstack); all of those bypass the section's own
          // createDOM loop where these affordances are normally
          // appended. Without this sweep, those rows would render
          // WITHOUT a delete button or drag handle while siblings
          // have one: the visible "only first row has no delete"
          // symptom users hit after an Undo.
          //
          // Cheap: O(N) querySelector calls, with the inner work
          // (re-appending) skipped via idempotent guards in the
          // helpers themselves. Done unconditionally per row so
          // index-based per-row state (aria-posinset, dataset.idx)
          // stays in sync after reorders / inserts / removes.
          const rows = body.querySelectorAll(':scope > *:not(.mar-drop-indicator):not(.mar-live-region)');
          if (newEdS) {
            for (let i = 0; i < rows.length; i++) {
              ensureRowEditAffordances(rows[i], i, rows.length);
            }
          }
          if (newOnDelS && newOnDelS.handler) {
            for (let i = 0; i < rows.length; i++) {
              attachRowDeleteAffordance(rows[i], i, newOnDelS.handler, newDelEdS);
            }
          }
        }
        break;
      }
      case 'uiList':
        // No reorder semantics on list itself; just diff children.
        patchChildrenPositional(node, oldView.children, newView.children, 'uiList');
        break;
      case 'form':
      case 'hstack':
      case 'vstack':
      default:
        patchChildrenPositional(node, oldView.children, newView.children, newView.tag);
    }
    return node;
  }

  // patchChildrenPositional walks DOM children and VView children in
  // lockstep. Same-tag pairs are patched in place; new entries are
  // appended; removed entries are detached.
  //
  // Dispatches to patchChildrenKeyed when any child carries a `key`
  // attr: preserves identity (focus, animation, scroll, custom
  // state on the DOM node) across reorders. Positional matching is
  // a fast path for the common case (homogeneous static-order
  // lists like form sections); keyed is the right thing for
  // anything that can reorder.
  function patchChildrenPositional(parentNode, oldChildren, newChildren, _parentTag) {
    if (hasAnyKey(oldChildren) || hasAnyKey(newChildren)) {
      patchChildrenKeyed(parentNode, oldChildren, newChildren);
      return;
    }
    const domChildren = parentNode.childNodes;
    const max = Math.max(oldChildren.length, newChildren.length);
    for (let i = 0; i < max; i++) {
      const newChild = newChildren[i];
      const domChild = domChildren[i];
      if (!newChild) {
        // Excess DOM nodes: remove the rest.
        while (parentNode.childNodes.length > newChildren.length) {
          parentNode.removeChild(parentNode.lastChild);
        }
        break;
      }
      if (!domChild) {
        // New child: append.
        parentNode.appendChild(createDOM(newChild));
        continue;
      }
      // Existing: patch in place.
      patchDOM(domChild, newChild);
    }
  }

  // hasAnyKey returns true if any view in the list declares a
  // `key` attr. Used by patchChildrenPositional to decide whether
  // to switch to keyed mode.
  function hasAnyKey(children) {
    for (const c of children) if (viewKey(c)) return true;
    return false;
  }

  // viewKey extracts the `key` attr value (or null) from a view.
  // Lives next to hasAnyKey so call sites pull identity in one
  // place, if we later move keys off the attrs list onto a
  // dedicated field of VView, only this one helper changes.
  function viewKey(view) {
    if (!view || !view.attrs) return null;
    for (const a of view.attrs) {
      if (a.name === 'key' && a.value && a.value.k === 'S') {
        return a.value.s;
      }
    }
    return null;
  }

  // patchChildrenKeyed reorders DOM children to match newChildren by
  // matching `key` attrs across renders. Steps:
  //
  //   1. Build a map from old key → existing DOM node.
  //   2. Walk newChildren in order. For each:
  //      - If the key exists in the map: reuse that DOM node,
  //        moving it via insertBefore to its new position, then
  //        patch its content in place. Mark it as "consumed".
  //      - If no key match (new entry): create a fresh DOM node.
  //      - If a new child has no key, fall back to creating fresh.
  //        (Mixing keyed and unkeyed siblings is a smell; we don't
  //        attempt clever matching: the unkeyed ones are treated
  //        as always-new.)
  //   3. After the walk, remove any old DOM nodes whose key wasn't
  //      consumed by the new list (i.e., the item was removed).
  //
  // The result: rows that moved keep their DOM node (and with it
  // focus, scroll, animation state), rows that vanished are removed,
  // rows that appeared are created.
  function patchChildrenKeyed(parentNode, oldChildren, newChildren) {
    const domChildren = Array.from(parentNode.childNodes);

    // Step 1: index old DOM nodes by key.
    const oldByKey = new Map();
    for (let i = 0; i < oldChildren.length; i++) {
      const k = viewKey(oldChildren[i]);
      if (k != null && domChildren[i]) {
        oldByKey.set(k, { node: domChildren[i], view: oldChildren[i] });
      }
    }

    // Step 2: walk newChildren in order, reordering DOM as we go.
    // `cursor` tracks the position where the next reused/created
    // node should land. We use insertBefore(node, anchor) where
    // anchor is the node currently at `cursor`: that's the only
    // primitive that handles "move to position N" correctly even
    // when the node is already somewhere else in the parent.
    const consumed = new Set();
    for (let i = 0; i < newChildren.length; i++) {
      const newChild = newChildren[i];
      const newK = viewKey(newChild);
      const anchor = parentNode.childNodes[i] || null;
      if (newK != null && oldByKey.has(newK)) {
        // Reuse: move the existing node to position i.
        const { node, view: oldView } = oldByKey.get(newK);
        consumed.add(newK);
        if (node !== anchor) {
          parentNode.insertBefore(node, anchor);
        }
        // Patch content in place (handles attr / text / children
        // changes; preserves the DOM identity we just moved).
        patchDOM(node, newChild);
      } else {
        // New entry: create fresh and insert at position i.
        const node = createDOM(newChild);
        parentNode.insertBefore(node, anchor);
      }
    }

    // Step 3: remove any old keyed nodes that weren't consumed.
    // Also remove any extra positional (unkeyed) leftovers past the
    // new length.
    for (const [k, entry] of oldByKey) {
      if (!consumed.has(k) && entry.node.parentNode === parentNode) {
        parentNode.removeChild(entry.node);
      }
    }
    while (parentNode.childNodes.length > newChildren.length) {
      parentNode.removeChild(parentNode.lastChild);
    }
  }

  // ---------- MVU loop ----------

  function unwrapModelTuple(v) {
    if (v && v.k === 'T' && v.xs.length === 2) {
      return { model: v.xs[0], effect: v.xs[1] };
    }
    return { model: v, effect: VEffect(() => VUnit(), 'none') };
  }

  function runEffect(eff) {
    if (eff && eff.k === 'E' && typeof eff.run === 'function') {
      try { eff.run(); } catch (e) { console.error('effect failed:', e); }
    }
  }

  // Listener bookkeeping that survives across mountPages calls.
  //
  // `mar dev`\'s hot reload re-invokes mountPages on every save
  // (see marReload → marRun). Without explicitly removing the
  // previous listeners, each call would stack ANOTHER popstate
  // listener on `window` and ANOTHER click delegator on
  // `#mar-root`. After N reloads, every `history.back()` would
  // fire N+1 render() closures in parallel, each tied to a stale
  // mountPages scope, racing to startViewTransition (only one is
  // allowed in flight: the rest reject silently). The visible
  // symptom is the back button needing several presses to
  // actually advance the URL.
  //
  // We track the most recent listener references at IIFE scope
  // and remove them on each new mountPages, so there\'s always
  // exactly one active listener of each kind.
  let prevPopstateHandler = null;
  let prevLinkClickHandler = null;
  let prevLinkClickRoot = null;

  // mountPages mounts a list of pages with URL-based routing. A
  // single-page app is just a list of one page (path "/"). Each page
  // has its own model: selected by window.location.pathname.
  // Falls back to the first page when the URL doesn't match any path.
  // iOS Safari needs a no-op touch listener on `document` to enter
  // its "delegated event delivery" mode. WITHOUT this, after a
  // long-touch gesture (like our drag reorder), Safari leaves the
  // page in a state where the next tap on any button is consumed
  // by gesture-state cleanup instead of firing the button's click
  // handler: operator must tap twice. The fix is documented voodoo
  // (the same trick FastClick used in the late 2010s, the same
  // reason jQuery Mobile shipped an empty body click handler):
  // adding a passive listener to `document` flips Safari onto a
  // code path that delivers events immediately after touchend
  // instead of waiting for the gesture state machine.
  //
  // Discovered empirically: when the operator pasted a console
  // snippet that attached debug listeners to `document`, the
  // double-tap bug vanished immediately. Reload (without the
  // snippet) brought the bug back. That's the signature of this
  // exact iOS Safari quirk.
  //
  // Installed once per page load (the iife wrapping the runtime
  // runs once per page). Hot-reload doesn't need to re-install
  // because the previous listener stays attached; even if a
  // second one were added, both being no-ops doesn't matter.
  if (typeof document !== 'undefined' && !document.__marTouchPrimer) {
    document.addEventListener('touchstart', function () {}, { passive: true });
    document.__marTouchPrimer = true;
  }

  function mountPages(pageList) {
    // A fresh mount (cold load or hot reload) owns the live timers; clear any
    // the previous mountPages left running so they do not double-fire.
    teardownAllSubs();
    // `halted` needs no reset here: it is declared inside this function, so a
    // hot reload re-enters mountPages and gets a fresh binding that starts
    // false. That is the developer saying "try again", and it costs nothing.
    const pages = {};
    let firstPath = null;

    // Cached Auth.me result for protected pages. null = not fetched
    // yet; VCtor('Just', [user]) | VCtor('Nothing', []) once resolved.
    // We refetch on demand (see ensureUser) and invalidate on logout.
    let authUser = null;
    let authPending = null;

    function fetchAuthMe() {
      if (authPending) return authPending;
      authPending = fetch('/_auth/whoami', { credentials: 'same-origin' })
        // Covers the app that navigates between protected pages without
        // ever calling a service: whoami is the only traffic it makes.
        .then(r => { noteServerIdentity(r); return r; })
        .then(r => r.ok ? r.text() : Promise.reject('HTTP ' + r.status))
        .then(t => {
          const raw = t ? JSON.parse(t) : null;
          authUser = raw == null
            ? VCtor('Nothing', [])
            : VCtor('Just', [jsToMar(raw)]);
          return authUser;
        })
        .catch(() => {
          authUser = VCtor('Nothing', []);
          return authUser;
        })
        .finally(() => { authPending = null; });
      return authPending;
    }

    // Does this browser hold an admin session? Tri-state, because "we have
    // not asked yet" and "asked, and no" lead to different renders: null =
    // unknown, true / false = answered. Mirrors authUser / fetchAuthMe.
    //
    // The answer is the server's to give: the session is an HttpOnly cookie,
    // so the only way to know is to make a request that the server refuses
    // without it. server-info is the cheapest of the admin routes and 401s
    // like the rest.
    let adminAuthed = null;
    let adminPending = null;

    function fetchAdminSession() {
      if (adminPending) return adminPending;
      adminPending = fetch('/_mar/admin/api/mar/server-info', {
        method: 'GET',
        credentials: 'same-origin',
      })
        .then(r => {
          // Only a 200 is a yes. Testing for `!== 401` would read a 404 as
          // authorized, and 404 is exactly what a frontend-only app returns:
          // it mounts no admin routes at all, so the page could never be
          // legitimately viewed there. Say so, because the redirect below
          // would otherwise land the operator on a second 404 with no clue
          // why — the same courtesy Page.protected pays a missing signInPage.
          if (r.status === 404) {
            console.error('[mar] Page.adminProtected used in an app with no admin panel. ' +
              'The panel is mounted by App.fullstack / App.backend; a frontend-only app has ' +
              'no admin session to gate on.');
          }
          adminAuthed = (r.status === 200);
        })
        // A network failure is not an authorization answer. Treat it as
        // "no" so the page does not render on a broken connection, but
        // leave nothing cached: the next render asks again.
        .catch(() => { adminAuthed = false; })
        .finally(() => { adminPending = null; });
      return adminPending;
    }

    // Path-pattern helpers (parsePathPattern, matchPathPattern,
    // buildPathURL) live at the IIFE level so they're reachable from
    // both makeBuiltinEnv (linkTo / Nav.pushTo) and mountPages.

    // `fns()` returns { init, update, view, subscriptions }: a function
    // rather than four values because a page built by Page.withShared has to
    // resolve them against the CURRENT shared model on every use. For an
    // ordinary page it closes over one ctor and never changes.
    function buildPageEntry(path, fns, title, isProtected, isDynamic, isSheet, isAdminOnly) {
      // For protected/dynamic pages init/update/view need extra args
      // threaded in (User, Params, or both). We can't init until those
      // are known, so we defer.
      const models = {};         // preservation key → model
      const initEffects = {};    // preservation key → first-render effect
      const initializedKeys = {}; // preservation key → bool

      // Apply User and Params in the right order (User first, then
      // Params: matches the type signatures in env.go). Either may
      // be null/undefined when irrelevant.
      const applyExtras = (fn, user, params) => {
        let f = fn;
        if (isProtected) f = apply(f, user);
        if (isDynamic)   f = apply(f, params);
        return f;
      };

      const initWith = (user, params, key) => {
        if (initializedKeys[key]) return;
        initializedKeys[key] = true;
        const initFnApplied = applyExtras(fns().init, user, params);
        // initFnApplied IS the (model, effect) tuple by now: plain
        // pages declare init as the tuple value itself, and the
        // protected/dynamic flavors became it once applyExtras fed
        // them User/Params. No vestigial unit argument. Unwrapping is
        // pure, the effect is only run if we go on to adopt it, so it
        // is safe to do here, before deciding whether to keep `prior`.
        const initial = unwrapModelTuple(initFnApplied);
        const prior = preservedScreenModels[key];
        // A preserved model is kept only if it still has the SHAPE this
        // program's init produces. Rendering it was the old test, and it
        // let a mismatch through whenever the view happened not to read
        // the field that changed: the failure then surfaced on the first
        // `update` that did, as a runtime error far from its cause.
        //
        // The comparison is the top-level field set only. A nested change
        // still slips past, which is why missingField() above exists: the
        // probe stops the common case, the message explains the rest.
        if (prior !== undefined && sameShape(prior, initial.model)) {
          const viewFnApplied = applyExtras(fns().view, user, params);
          try {
            apply(viewFnApplied, prior);
            models[key] = prior;
            return;
          } catch (_) {
            if (typeof console !== 'undefined') {
              console.info('[mar reload] page ' + path + ' view rejected the kept model, fresh init');
            }
          }
        } else if (prior !== undefined && typeof console !== 'undefined') {
          console.info('[mar reload] page ' + path + ' model shape changed, fresh init');
        }
        models[key] = initial.model;
        initEffects[key] = initial.effect;
        preservedScreenModels[key] = initial.model;
      };

      const entry = {
        path,
        title,
        isProtected,
        isDynamic,
        // Page.sheet: presented OVER the page navigated from, rather
        // than replacing it. Only render()'s paint step reads this:
        // routing, auth, init, dispatch and subscriptions are identical
        // to any other page.
        isSheet: !!isSheet,
        // Page.adminProtected: the panel's own session gates this, and the
        // gate is UX (ADR 0031) — the page's code shipped to every visitor
        // in program.json long before render() ran.
        isAdminOnly: !!isAdminOnly,
        // Per-key state. activeKey is set by currentPage() before
        // render touches model / params: keeping these as getters
        // means the rest of the runtime stays oblivious to the
        // single-vs-many model split.
        activeKey: path,
        params: null,
        get model()       { return models[entry.activeKey]; },
        set model(v) {
          models[entry.activeKey] = v;
          preservedScreenModels[entry.activeKey] = v;
        },
        get initEffect()  { return initEffects[entry.activeKey] || null; },
        set initEffect(v) { initEffects[entry.activeKey] = v; },
        get initialized() { return !!initializedKeys[entry.activeKey]; },
        // Forget everything this runtime knows about one preservation
        // key, so the next render treats it as a page never visited.
        // ALL of it, in one place and one call: this state is spread
        // across five maps in three scopes, and clearing four of them
        // is how a "reset" page silently came back with its old model
        // still in `models`. Callers get no say in the subset.
        resetKey: (key) => {
          initializedKeys[key] = false;
          delete models[key];
          delete initEffects[key];
          delete preservedScreenModels[key];
          delete initEffectsRun[key];
          // A fresh model is a new showing of the page, and its sources hand
          // it their state whatever it was last handed (pageSubOwner,
          // pageStateSeen).
          if (key === subPageKey) subPageKey = null;
          forgetPageStates(key);
        },
        initWith: (user, params) => initWith(user, params, entry.activeKey),
        // After a hot reload a covered entry's model is only in
        // preservedScreenModels until the entry is shown again.
        modelAt: (key) => (models[key] !== undefined ? models[key] : preservedScreenModels[key]),
        setModelAt: (key, v) => { models[key] = v; preservedScreenModels[key] = v; },
        // Getters, not values: under Page.withShared these resolve against
        // the shared model as it is right now, so a store change repaints
        // with the new view without the page's own model moving.
        get init()          { return fns().init; },
        get update()        { return fns().update; },
        get view()          { return fns().view; },
        get subscriptions() { return fns().subscriptions; },
      };

      return entry;
    }

    // Static pages live in the `pages` map (literal path → entry).
    // Dynamic pages live in `dynamicPages` as an ordered list of
    // {pattern, page} pairs. Resolution order: static first (so a
    // literal '/notes/new' beats the pattern '/notes/:id'), then
    // dynamic in declaration order.
    const dynamicPages = [];

    // The page ctors all share the arg shape [path, init, update, view,
    // title, subscriptions], so one reader serves all four.
    const ctorFns = (c) => ({ init: c.args[1], update: c.args[2], view: c.args[3], subscriptions: c.args[5] });

    for (const p0 of pageList) {
      if (p0.k !== 'C') continue;

      // Page.withShared wraps any of the four ctors below, or another
      // Page.withShared: a page that reads several stores. Unwrap every layer
      // here so everything downstream, routing, auth, init, dispatch, subs,
      // sees an ordinary page and stays oblivious to shared entirely.
      //
      // Each layer's builder is applied to its store's live model, outermost
      // first. The page is rebuilt only when one of those models actually
      // changed; identity is enough because Mar models are immutable, so a
      // new model is always a new object.
      //
      // Page.sheet can mark any layer, the outside of a Page.withShared as
      // well as the page inside it, so every layer is asked.
      let p = p0, fns = null, sheetLayer = false;
      if (p0.tag === '__SharedPage') {
        let built = null, readFrom = null;   // [{store, model}] the page was built from
        const rebuild = () => {
          if (readFrom !== null && readFrom.every((r) => r.store.model === r.model)) return built;
          const reads = [];
          let layer = p0;
          while (layer.k === 'C' && layer.tag === '__SharedPage') {
            if (layer.presented === true) sheetLayer = true;
            const store = sharedStoreFor(layer.args[0]);
            reads.push({ store, model: store.model });
            layer = apply(layer.args[1], store.model);
          }
          built = layer;
          readFrom = reads;
          return built;
        };
        p = rebuild();
        fns = () => ctorFns(rebuild());
      }

      if (p.tag !== '__Page' && p.tag !== '__ProtectedPage' &&
          p.tag !== '__DynamicPage' && p.tag !== '__DynamicProtectedPage') continue;
      // These ctors share the same arg shape: [path, init, update, view,
      // title]. __ProtectedPage / __DynamicProtectedPage trigger the auth
      // gate (redirect resolved from globalThis.__marAuthSignInPath, set by
      // the Auth.config builtin). __DynamicPage / __DynamicProtectedPage also
      // enable URL-pattern matching with `:param` segments. (Page.adminProtected
      // pre-applies its AdminSession and emits a plain __Page: see its builtin.)
      const pathV = p.args[0], titleV = p.args[4];
      const path = pathV.s;
      const title = (titleV && titleV.k === 'S') ? titleV.s : '';
      const isProtected = (p.tag === '__ProtectedPage' || p.tag === '__DynamicProtectedPage');
      const isDynamic   = (p.tag === '__DynamicPage'   || p.tag === '__DynamicProtectedPage');
      // Presentation rides as a property (set by Page.sheet), not a tag
      // or an arg slot, so it composes with all four ctors above.
      const isSheet     = sheetLayer || p.presented === true;
      const isAdminOnly = p.adminOnly === true;
      if (firstPath === null) firstPath = path;
      // Path, title and the flags are read ONCE: they are the route, and a
      // route that moved when the shared model changed would be a different
      // page, not the same page redrawn. Only the four functions are live.
      const entry = buildPageEntry(path, fns || (() => ctorFns(p)), title, isProtected, isDynamic, isSheet, isAdminOnly);
      if (isDynamic) {
        dynamicPages.push({ pattern: parsePathPattern(path), page: entry });
      } else {
        pages[path] = entry;
      }
    }

    let initEffectsRun = {};

    // Resolve the URL to a page entry. Side effect: stamps
    // entry.activeKey + entry.params so the rest of the runtime sees
    // a per-URL model slot (dynamic pages) and the right Params record.
    function currentPage() {
      const urlPath = window.location.pathname;
      // 1) literal match
      if (pages[urlPath]) {
        const pg = pages[urlPath];
        pg.activeKey = pg.path + '#' + curEntryId();
        pg.params = null;
        return pg;
      }
      // 2) dynamic-pattern match, in declaration order
      for (const dp of dynamicPages) {
        const params = matchPathPattern(urlPath, dp.pattern);
        if (params !== null) {
          // Dynamic pages key model state by URL, not by path: navigating
          // /notes/abc → /notes/xyz is a fresh model slot, not a re-render.
          // (Static pages key by path: see the literal match above.)
          dp.page.activeKey = urlPath + '#' + curEntryId();
          dp.page.params = params;
          return dp.page;
        }
      }
      // 3) fallback to the first declared STATIC page (single-page apps
      //    with a stale URL, dev hot-reload from a deep link, etc.).
      //
      //    Never a dynamic one. It used to fall through to
      //    `dynamicPages[0]` and then stamp `params = null`, which is
      //    right for a static page and a guaranteed crash for a dynamic
      //    one: its `view` reads the params the URL was supposed to
      //    carry. Measured, on an app whose only page was
      //    `/note/{id:Int}`, every non-matching URL — including `/` —
      //    painted "view failed: Cannot read properties of null".
      //
      //    iOS never had this: it refuses to fall back at all and shows
      //    "Page not found" (ContentView.matchPath). An app with no
      //    static page is now refused at boot, so this returns null only
      //    for a program that could not have started.
      const fallback = pages[firstPath] || null;
      if (fallback) {
        fallback.activeKey = fallback.path + '#' + curEntryId();
        fallback.params = null;
      }
      return fallback;
    }

    // The screen a presented route sits over, as a concrete url.
    //
    // Longest declared route that is a proper PATH PREFIX of this one:
    // /classes/3/attendance yields /classes/3, params and all, because the
    // prefix of a concrete url is itself concrete. Routes that nest on screen
    // nest in the url too, if they don't, that is a shape worth noticing in
    // the route table rather than a case to paper over here.
    //
    // A presented route with no such parent still gets the app's first page,
    // because a modal always has something behind it. Returns null only when
    // there is nothing at all to put there, which is a single-page app whose
    // only page is a sheet.
    //
    // Resolution is deliberately read-only: currentPage() stamps activeKey and
    // params onto the page it returns, and doing that to a page we are only
    // asking about would hand the renderer a half-selected route.
    function routeAt(urlPath) {
      if (pages[urlPath]) return pages[urlPath];
      for (const dp of dynamicPages) {
        if (matchPathPattern(urlPath, dp.pattern) !== null) return dp.page;
      }
      return null;
    }
    function coveredPathFor(urlPath) {
      const segments = urlPath.split('/').filter(Boolean);
      for (let n = segments.length - 1; n > 0; n--) {
        const candidate = '/' + segments.slice(0, n).join('/');
        const pg = routeAt(candidate);
        if (pg && !pg.isSheet) return candidate;
      }
      const root = routeAt(firstPath);
      if (root && !root.isSheet && firstPath !== urlPath) return firstPath;
      return null;
    }

    let mounted = null;
    let mountedPath = null;   // the LIVE page in the DOM (drives the on-nav init cleanup)
    // Runtime-failure state (ADR 0020). Declared up here with the rest of the
    // render bookkeeping rather than beside the functions that use it, because
    // render() runs several times before that point in this body: a `let`
    // further down would be in its temporal dead zone.
    let dispatchErrorBanner = null;  // the banner over a still-usable screen
    let pageFailure = null;          // signature of the failure currently painted
    let halted = false;              // a view failed; the program is stopped (ADR 0028)
    // A presented route (Page.sheet) paints into an overlay while the
    // page it was reached from stays untouched in #mar-root.
    let coveredKey = null;     // page left painted UNDERNEATH the presentation
    let presentedKey = null;   // route currently painted in the overlay
    let presentedDOM = null;   // its root node, kept for patching
    let presentedHost = null;  // the .mar-sheet-backdrop we own
    let drawnPath = null;     // the page currently PAINTED: differs from mountedPath only
                              // while time-travel draws a past frame from another page
    // The page a View Transition is about to swap in, and which swap is the
    // latest. Between startViewTransition and its callback (a frame later) the
    // DOM still shows the page being left, on purpose: that is what the browser
    // captures as the old state. A render for the same page in that window (a
    // message the page received: a seed, a tick, a fetch) must not swap it in
    // early, or the browser captures the new page as the old one and the slide
    // animates the page onto itself; the pending swap builds from the live
    // model anyway. A render for another page (Back during a push's slide)
    // supersedes the pending swap, which then does nothing.
    let pendingSwapKey = null;
    let swapSeq = 0;
    let routerInstalled = false;
    // Tracks the marDepth at the last DOM-swap render. The next swap
    // compares newDepth vs this to pick the view-transition direction
    // (forward / back / none).
    //
    // `firstRender` short-circuits the direction computation on the
    // very first render of this mountPages call: there is no previous
    // depth to compare against. Without this, a cold load on a deep
    // sub-page (e.g. /projects/42, marDepth=2, restored from BFCache
    // or browser session) would compute newDepth(2) > lastSeen(0) →
    // 'forward' and play a slide-in-from-right on top of the initial
    // mount. Same trap on hot-reload (the IIFE re-runs with a fresh
    // lastSeenNavDepth while history.state preserves the real depth).
    let lastSeenNavDepth = 0;
    let firstRender = true;

    // Returns the User value for a protected page, or null if we
    // either don't have an answer yet or the user isn't logged in.
    // `authUser` (mountPages-scoped) is VCtor('Just', [user]) | VCtor('Nothing', []) | null.
    function getUser() {
      if (!authUser || authUser.k !== 'C') return null;
      if (authUser.tag === 'Just' && authUser.args.length > 0) return authUser.args[0];
      return null;
    }

    // For protected/dynamic pages init/update/view take extras as the
    // leading args (User first, then Params, mirroring the type sigs
    // in env.go). These helpers thread them in transparently so the
    // rest of the runtime (render, dispatch, time-travel) can stay
    // agnostic to the four page flavors.
    function applyExtras(pg, fn) {
      let f = fn;
      if (pg.isProtected) f = apply(f, getUser());
      if (pg.isDynamic)   f = apply(f, pg.params);
      return f;
    }
    function viewWithUser(pg, model) {
      return apply(applyExtras(pg, pg.view), model);
    }
    function updateWithUser(pg, msg, model) {
      return apply(apply(applyExtras(pg, pg.update), msg), model);
    }

    // When the user agent already animated this traversal itself
    // (Safari macOS two-finger swipe-back is the canonical case),
    // we skip our own startViewTransition for the next render so
    // animations don't stack. The flag is set by the popstate
    // handler below: it reads PopStateEvent.hasUAVisualTransition,
    // a standard property browsers expose during traversal-driven
    // visual transitions. Reset to false after a single render.
    let skipNextViewTransition = false;

    // Tracks WHICH nav verb triggered the upcoming render so we can
    // pick a direction-correct animation. Depth alone isn't enough:
    //
    //   - Nav.replace    → same depth, but it's a context change
    //     (logout, switching modes); use 'fade' (no direction).
    //   - Nav.replaceFresh → depth resets to 0; semantically it's
    //     ALSO a context change (sign-in completed, redirected
    //     after auth-expired), not a "go back": use 'fade'.
    //   - Nav.push       → depth grows; use 'forward' slide.
    //   - browser back   → depth shrinks; use 'back' slide.
    //
    // The depth heuristic alone would render replaceFresh as 'back'
    // (because newDepth < lastSeen), which implies "going back into
    // history": visually wrong since the previous flow is gone.
    // Set just before render() by the nav primitives below; read
    // and cleared inside render().
    let pendingNavKind = null;

    // Classifies the navigation about to be rendered. Two things read
    // this and they must never disagree: the slide animation (which way
    // the screens move) and the re-init guard (whether the destination
    // gets a fresh model or its old one back). A user who SEES a back
    // slide must GET the screen they left, so both answers come from
    // here, from one set of rules.
    //
    //   'none'    - first render of this mountPages call (cold load,
    //               direct URL, hot reload): there is no previous
    //               screen to have left. Also same-depth key changes.
    //   'fade'    - Nav.replace / Nav.replaceFresh: a context change
    //               (logout, sign-in completed), not a stack movement.
    //   'forward': Nav.push and anything else that grows the depth.
    //   'back'    - the depth shrank: browser Back, swipe-back, or a
    //               history.back() from app code.
    //
    // Depth is a counter, so one case it cannot see: the browser's
    // FORWARD button after a Back. That grows the depth exactly like a
    // push, so the screen re-inits rather than being restored. Telling
    // the two apart would mean stamping a monotonic id on every history
    // entry: real machinery for a button that app-shaped UIs rarely
    // show and iOS doesn't have at all. Left as is, deliberately.
    //
    // Pure: reads state, mutates none. render() calls it twice in one
    // pass (guard first, swap later) and both must get the same answer,
    // so the bookkeeping it depends on: firstRender, lastSeenNavDepth,
    // pendingNavKind, is only advanced by the swap, at the end.
    function navKind() {
      if (firstRender) return 'none';
      if (pendingNavKind === 'replace' || pendingNavKind === 'replaceFresh') return 'fade';
      const newDepth = currentNavDepth();
      if (newDepth > lastSeenNavDepth) return 'forward';
      if (newDepth < lastSeenNavDepth) return 'back';
      return 'none';
    }

    // Games own the whole page: when the mounted root view is a canvas,
    // extend .mar-canvas's selection armor to <html>/<body> (see the
    // html.mar-canvas-page CSS). Toggled, not just added, so SPA
    // navigation from a game page back to a regular UI page restores
    // normal text selection.
    function syncCanvasRootClass() {
      const isGame = !!(mounted && mounted.classList
        && mounted.classList.contains('mar-canvas'));
      document.documentElement.classList.toggle('mar-canvas-page', isGame);
    }

    // Intercepts clicks on in-app links so navigation stays in-process.
    // Delegated, so one listener per host covers every descendant anchor.
    // Named at this scope (rather than inline where it is installed)
    // because there are two hosts: #mar-root and, when a route is
    // presented, the sheet overlay, which is not inside #mar-root.
    const onLinkClick = (ev) => {
      const a = ev.target.closest && ev.target.closest('a[href^="/"]');
      if (!a) return;
      // Disabled navigationLink: swallow the click so the browser
      // doesn't follow the href (full reload) and skip the SPA push.
      // Mirrors how disabled <button> suppresses dispatch.
      const lv = a.__marView;
      if (lv && isDisabled(lv)) {
        ev.preventDefault();
        return;
      }
      const href = a.getAttribute('href');
      if (matchesAnyPage(href)) {
        ev.preventDefault();
        pushNav(href);
        render();
      }
    };

    // ---------- Presented routes (Page.sheet) ----------
    //
    // Same page machinery as everything else: same routing, same auth
    // gate, same init, same dispatch, same subscriptions. The ONLY thing
    // that differs is where the view is painted: into a sheet over the
    // previous page instead of into #mar-root in its place.
    //
    // Dismissal always goes through history, never straight to the DOM.
    // The URL is what says which route is showing, so letting a click
    // remove the panel directly would leave the two disagreeing (Back
    // would then "return" to a page already on screen). history.back()
    // makes popstate the single path down, shared with the hardware /
    // browser back button.
    function presentedPanel() {
      if (presentedHost) return presentedHost.querySelector('.mar-sheet-panel');
      ensureUIStyles();
      const host = document.createElement('div');
      host.className = 'mar-sheet-backdrop mar-page-sheet';
      const panel = document.createElement('div');
      panel.className = 'mar-sheet-panel';
      const handle = document.createElement('div');
      handle.className = 'mar-sheet-handle';
      handle.setAttribute('aria-hidden', 'true');
      panel.appendChild(handle);
      host.appendChild(panel);
      document.body.appendChild(host);
      host.addEventListener('click', (ev) => {
        // Only the backdrop itself: clicks that bubbled up from the
        // panel's own content are not a dismissal.
        if (ev.target === host) dismissPresented();
      });
      // Links inside the sheet route are still app links, and the
      // delegated interceptor lives on #mar-root, which is not an
      // ancestor of this host.
      host.addEventListener('click', onLinkClick);
      presentedHost = host;
      // Force a style flush, THEN open. The browser needs a computed
      // closed state to transition from (same reason the backdrop is
      // never display:none), reading a layout property provides it
      // synchronously. requestAnimationFrame would also work while the
      // tab is visible, but its callbacks are parked in a background
      // tab, and a sheet that stays at opacity 0 with pointer-events
      // none is a screen the user cannot see OR dismiss.
      void host.offsetHeight;
      host.classList.add('mar-sheet-open');
      return panel;
    }

    function paintPresented(pg, viewVal) {
      const panel = presentedPanel();
      if (presentedKey !== pg.activeKey) {
        // Different route than the one showing (first present, or one
        // sheet route replaced by another): rebuild, keeping the handle.
        while (panel.lastChild && panel.childNodes.length > 1) panel.removeChild(panel.lastChild);
        presentedDOM = createDOM(viewVal);
        panel.appendChild(presentedDOM);
        presentedKey = pg.activeKey;
        panel.scrollTop = 0;
      } else {
        presentedDOM = patchDOM(presentedDOM, viewVal);
      }
    }

    function dismissPresented() {
      if (presentedKey === null) return;
      history.back();
    }

    function closePresented() {
      presentedKey = null;
      presentedDOM = null;
      coveredKey = null;
      const host = presentedHost;
      presentedHost = null;
      if (!host) return;
      host.classList.remove('mar-sheet-open');
      const drop = () => { if (host.parentNode) host.parentNode.removeChild(host); };
      // Detach after the exit transition. A timer rather than
      // transitionend because reduced motion (and a hidden tab) can mean
      // no transition ever fires, and then the host would linger over
      // the page forever, swallowing clicks.
      if (prefersReducedMotion()) drop();
      else setTimeout(drop, 340);
    }

    // Escape closes the presented route. Bound once at the document
    // level (not on the host) because focus is usually inside a field in
    // the sheet, and keydown on the host would miss keys sent to it.
    if (!globalThis.__marPresentedEscBound) {
      globalThis.__marPresentedEscBound = true;
      document.addEventListener('keydown', (ev) => {
        if (ev.key !== 'Escape') return;
        if (globalThis.__marDismissPresented) globalThis.__marDismissPresented();
      });
    }
    globalThis.__marDismissPresented = dismissPresented;
    // Published so Nav.dismiss (defined with the builtins, outside this
    // scope) can tell "close the sheet" from "step back one screen".
    globalThis.__marPresentedActive = () => presentedKey !== null;

    // The page-level error boundary (ADR 0020). Everything drawing a screen
    // goes through here: `init`, `subscriptions`, `view` and the DOM builder.
    // A throw from any of them means the same thing to the person looking at
    // it, this screen could not be shown, so they get the same treatment.
    function render() {
      try {
        renderPage();
        pageFailure = null;
      } catch (err) {
        presentPageFailure(err);
      }
    }

    function renderPage() {
      // An entry with no id (made before ids existed) gets one of its own.
      // An entry that left the stack never comes back (browser Forward after
      // Back lands on it again): it arrives as a new entry, the same new
      // entry for every history entry that carried the old id.
      const stamped = history.state && history.state.marEntry;
      if (!stamped || retiredEntries.has(stamped)) {
        let fresh = null;
        if (stamped) {
          let r = stamped;
          for (let hops = 0; restampedEntries.has(r) && hops < 64; hops++) r = restampedEntries.get(r);
          fresh = (r !== stamped && !retiredEntries.has(r)) ? r : newEntryId();
          if (fresh !== r) restampedEntries.set(r, fresh);
          restampedEntries.set(stamped, fresh);
        } else {
          fresh = newEntryId();
        }
        history.replaceState(Object.assign({}, history.state, { marEntry: fresh }), '',
          window.location.pathname + window.location.search + window.location.hash);
      }
      const pg = currentPage();
      if (!pg) return;
      syncLiveStack(curEntryId(), currentNavDepth(), pg.activeKey);

      // ── Re-init on navigation: push is fresh, pop restores ──
      // Going somewhere NEW runs that page's `init` from scratch, as if
      // it were the first visit, and re-fires its init effect, so you
      // never open a screen showing the previous visit's stale data.
      // Going BACK restores the screen you left, model intact, no
      // refetch. That's a navigation stack: the pages below the top
      // didn't stop existing while you were away.
      //
      // The two halves are one rule, not a special case each: whether
      // this is a push or a pop is the same answer that decides which
      // way the screens slide (see navKind). What you see and what you
      // get can't drift apart.
      //
      // This also matches iOS, where it falls out of the platform:
      // NavigationStack keeps each pushed MarPageHost alive with its
      // own @State PageRuntime, so popping hands the live runtime back.
      // The web used to re-init unconditionally, which was the one real
      // behavioral gap between the two runtimes.
      //
      // We detect a genuine navigation, not a same-page re-render, and
      // not a hot-reload remount: with two facts read from the PREVIOUS
      // render: a page was already `mounted`, AND the active key changed.
      //
      // Hot reload (marReload) is unaffected: it re-runs mountPages with a
      // null `mounted`, so this guard is skipped and the module-level
      // preservedScreenModels is reused: you keep your screen across saves.
      // Time-travel keeps its own frame history, untouched here.
      //
      // To share data across screens (instead of refetching per page),
      // lift it out of the page model: the server is the shared source in
      // fullstack apps.
      if (mounted != null && mountedPath !== pg.activeKey) {
        if (navKind() !== 'back') pg.resetKey(pg.activeKey);
        // Claim the navigation HERE, not in swap(), Back included. swap()
        // also assigns mountedPath, but it runs after this render computes
        // a view and (under View Transitions) a frame later still, while
        // this render has already advanced the nav depth. Anything that
        // dispatches in between re-enters render() with the depth caught
        // up, so it no longer reads as 'back', and with mountedPath still
        // pointing at the PREVIOUS page it looks like a new arrival:
        //
        // - An init effect that dispatches WITHOUT waiting on the network
        //   (`Cmd.perform GotToday Time.now` has its value immediately):
        //   the nested render reset the key it had just initialized and ran
        //   init again, which fired the effect again. Unbounded recursion:
        //   a frozen UI and thousands of identical requests from one tap.
        // - Any message during Back's transition (a timer tick, a fetch
        //   result, the device's state handed to the page shown again):
        //   the nested render reset the page Back had just restored.
        //
        // Assigning it now makes the guard idempotent per navigation,
        // which is all it ever meant: decide ONCE, when the destination
        // changes, whether the page starts over.
        mountedPath = pg.activeKey;
      }

      // Page.adminProtected gating, same shape as the Page.protected block
      // below: ask once, bail while we don't know, send the operator to the
      // panel's login when the answer is no.
      //
      // This is UX and nothing more (ADR 0031). The page's code is already in
      // the visitor's hands — program.json carries every page's view and its
      // literals — so this cannot keep a secret and must not be relied on to.
      // It exists so an operator who is signed out gets the login screen
      // rather than an ops page that renders and then fails on every fetch.
      //
      // The probe is the same cookie the Mar.Admin.* calls ride on, asked of
      // the cheapest introspection route. `credentials: same-origin` matters:
      // the session is an HttpOnly cookie the client cannot read directly.
      if (pg.isAdminOnly) {
        if (adminAuthed === null) {
          fetchAdminSession().then(() => render());
          return;
        }
        if (adminAuthed === false) {
          // Outside the SPA's router, so a real navigation rather than
          // replaceNav. Guarded the same way the Mar.Admin.* 401 path is,
          // so several pages resolving at once redirect once.
          if (!globalThis.__marAdminRedirecting) {
            globalThis.__marAdminRedirecting = true;
            window.location.assign('/_mar/admin/login');
          }
          return;
        }
      }

      // Page.protected gating: ensure we know who the user is before
      // mounting. If unauthed, navigate to the sign-in path declared
      // in Auth.config.signInPage. If we don't have an answer yet,
      // fire Auth.me and bail: the .then handler will re-call
      // render() once authUser is populated.
      if (pg.isProtected) {
        if (authUser == null) {
          fetchAuthMe().then(() => render());
          return;
        }
        const user = getUser();
        if (user == null) {
          const signInPath = globalThis.__marAuthSignInPath;
          if (!signInPath) {
            console.error('[mar] Page.protected used but Auth.config has no `signInPage`. ' +
              'Add `signInPage = Frontend.SignIn.page` to Auth.config so unauthed users have somewhere to go.');
            return;
          }
          // Replace history (no back-button to a page the user can't
          // see) and route to the sign-in page. Marked as 'replace'
          // so the cross-fade animation fires: the protected page
          // never visibly mounted, so a slide would be misleading.
          pendingNavKind = 'replace';
          replaceNav(signInPath);
          render();
          return;
        }
        if (!pg.initialized) {
          pg.initWith(user, pg.params);
        }
      } else if (pg.isDynamic) {
        // Public dynamic pages still need a deferred init: each URL is
        // its own model slot so the first time we see /notes/:id with a
        // new id, we have to run init with that Params record.
        if (!pg.initialized) {
          pg.initWith(null, pg.params);
        }
      } else {
        // Static public pages init the first time their stack entry is
        // shown, like every other page: each entry has a model of its own
        // (see newEntryId), so a page pushed again is a new entry and
        // starts over, and Back finds the entry it left as it was.
        if (!pg.initialized) {
          pg.initWith(null, null);
        }
      }

      if (!initEffectsRun[pg.activeKey] && pg.initEffect !== null) {
        initEffectsRun[pg.activeKey] = true;
        // AN INIT EFFECT CAN NAVIGATE, AND THAT RE-ENTERS THIS FUNCTION.
        //
        // Redirecting out of `init` is a supported pattern and the natural
        // way to write a bootstrap gate: "no handle yet, go and pick one"
        //, because deciding in the view means a flash of the page you were
        // never meant to see. So the effect below can call Nav.replaceTo,
        // which navigates and calls render() itself, and that nested pass
        // runs to completion before this line returns.
        //
        // Everything decided above (which page, which model, which user)
        // describes the route we have just left. Carrying on paints it
        // anyway, over the top of what the nested pass drew, using state
        // the nested pass was entitled to invalidate. The reported bug:
        // signing up put you on a protected page whose init redirected to
        // /setup, and the outer render went on to draw the page you had
        // left with a user it no longer knew: the view threw, the failure
        // screen flashed, and the app was wedged with the DOM and the
        // runtime disagreeing about which page was mounted.
        //
        // Every navigation path ends in its own render(), so stopping here
        // drops nothing: the screen the app actually moved to has already
        // been drawn, or is waiting on a fetch that will draw it.
        const pathBefore = window.location.pathname;
        runEffectAs(issuerFor(pg), pg.initEffect);
        if (window.location.pathname !== pathBefore) return;
      }
      // Per-page browser-tab title. Empty title (the default when the
      // user omits `title` from Page.create) leaves whatever the host
      // HTML had alone: useful in cases where a non-mar wrapper sets
      // a richer title.
      if (pg.title && document.title !== pg.title) {
        document.title = pg.title;
      }
      // Reconcile subscriptions against the current model: this runs on every
      // render (post-update, post-init, and on navigation), so leaving a page
      // stops its timers and the new page's subscriptions start.
      // Subscriptions reconcile against the LIVE model. While the dev panel is
      // inspecting a past frame (traveling), strip the clock sources so the
      // world's autonomous timers freeze: non-clock subs keep running.
      let desiredSub = pg.subscriptions ? apply(applyExtras(pg, pg.subscriptions), pg.model) : null;
      // Stamp the page's items with this showing of the page (pageSubOwner).
      // Copies, never in place: a top-level `subs = Device.watch Got` is one
      // value that every page using it returns.
      const pageOwner = pageSubOwner(pg.activeKey);
      if (desiredSub && desiredSub.items && desiredSub.items.length) {
        desiredSub = { k: 'SUB', items: desiredSub.items.map((it) => Object.assign({}, it, { owner: pageOwner })) };
      }
      // Shared subs belong to the STORE, not the screen, so they join every
      // render's desired set instead of starting and stopping with the page.
      // A `Time.every 1000` here is an app-wide heartbeat.
      //
      // The reconciler merges by key, so a shared Time.every 1000 and a
      // page's own are one timer with two taggers, and each tagger knows
      // which update loop it feeds (see deliverSub).
      if (sharedStores.size > 0) {
        let sharedItems = [];
        for (const store of sharedStores.values()) sharedItems = sharedItems.concat(sharedSubItems(store));
        if (sharedItems.length) {
          const own = (desiredSub && desiredSub.items) ? desiredSub.items : [];
          desiredSub = { k: 'SUB', items: own.concat(sharedItems) };
        }
      }
      // Freeze the world's clock sources whenever the time-travel panel is OPEN
      // (so opening it pauses at the present) or you've jumped to a past frame.
      if (preservedTimeTravel && (preservedTimeTravel.traveling || timeTravelPanelOpen())) desiredSub = stripClockSubs(desiredSub);
      reconcileSubs(desiredSub);
      // A failure this page's update had while it was covered shows now that
      // the page is on screen (see pendingPageErrors).
      if (pendingPageErrors.has(pg.activeKey)) {
        const err = pendingPageErrors.get(pg.activeKey);
        pendingPageErrors.delete(pg.activeKey);
        reportRuntimeError('update', err);
      }
      // Read-only time-travel: while inspecting, DRAW the selected frame's
      // snapshot, but never touch pg.model: the live model keeps advancing
      // underneath (async results append at the tail). Only applies when the
      // inspected frame belongs to the page we're on; a cross-page frame just
      // shows the live model (it can't reflect visibly here anyway).
      // If the frame was recorded on ANOTHER page, draw THAT page: time-travel
      // should show the app as it was, including which screen you were on. The
      // reported bug: a cross-page frame used to keep showing the live page, so
      // scrubbing back across a navigation looked like "nothing happened". Subs
      // still reconcile against the live `pg` above (clocks frozen while
      // traveling), so only what's PAINTED changes, never the live route.
      let viewPg = pg;
      let drawModel = pg.model;
      const isTraveling = !!(preservedTimeTravel && preservedTimeTravel.traveling && preservedTimeTravel.frames.length > 0);
      if (isTraveling) {
        const _c = preservedTimeTravel.cursor;
        const _f = _c === -1 ? preservedTimeTravel.frames[0] : preservedTimeTravel.frames[_c];
        if (_f) {
          const _fModel = _c === -1 ? _f.prevModel : _f.nextModel;
          if (_f.pagePath === pg.activeKey) {
            drawModel = _fModel;
          } else {
            const _fPg = lookupPageForFrame(_f.pagePath);
            if (_fPg) { viewPg = _fPg; drawModel = _fModel; }
          }
        }
      }
      const viewVal = viewWithUser(viewPg, drawModel);
      const root = document.getElementById('mar-root');
      // On page change, throw away the old DOM: diffing across a
      // navigation gives no useful work. Use activeKey (URL-based for
      // dynamic pages) so /notes/abc → /notes/xyz also resets the DOM.
      // Presentation (Page.sheet). Laying a route over another one needs
      // another one to lay it over: reached by navigation there is a page
      // already painted, but opened cold: deep link, reload, shared URL
      //: there is nothing behind it, and the route renders as an
      // ordinary full-screen page. Same URL, same model, whole screen.
      const presenting = viewPg.isSheet
        && (coveredKey !== null || (mounted != null && drawnPath !== viewPg.activeKey));
      if (presenting && coveredKey === null) coveredKey = drawnPath;
      if (!presenting && presentedKey !== null) {
        closePresented();
        // The covered page is the live one again. Normally the DOM swap
        // re-stamps mountedPath, but revealing a covered page takes the
        // patch path below (its DOM never left), so nothing else would:
        // and a stale mountedPath would stop the NEXT presentation from
        // re-initializing.
        mountedPath = pg.activeKey;
      }

      if (presenting) {
        paintPresented(viewPg, viewVal);
        // Advance the same nav bookkeeping the swap advances. Skipping it
        // would leave lastSeenNavDepth at the covered page's depth, so
        // the dismissal would read as 'none' instead of 'back', and the
        // re-init guard would wipe the covered page's model instead of
        // handing it back.
        firstRender = false;
        lastSeenNavDepth = currentNavDepth();
        pendingNavKind = null;
      } else if (pendingSwapKey !== null && pendingSwapKey === viewPg.activeKey && !isTraveling) {
        // The page is already on its way in (see pendingSwapKey).
        pendingNavKind = null;
      } else if (mounted == null || (pendingSwapKey !== null ? pendingSwapKey : drawnPath) !== viewPg.activeKey) {
        // The slide direction, from the same classification the
        // re-init guard used at the top of this render (see navKind).
        // 'forward' slides in from the right with the old screen
        // parallaxing left; 'back' reverses it; 'fade' cross-fades
        // with a subtle scale: iOS-modern, what Photos and Sign in
        // with Apple do between non-hierarchical contexts, reading as
        // "you stepped into a new place" without implying a stack
        // direction; 'none' doesn't animate.
        //
        // This is the point where the nav bookkeeping advances, and it
        // has to stay the only one: navKind() is pure precisely so the
        // guard above and this block agree within a single render.
        const newDepth = currentNavDepth();
        const dir = navKind();
        firstRender = false;
        lastSeenNavDepth = newDepth;
        pendingNavKind = null;
        // Scroll-to-top policy. Native iOS NavigationStack pushes
        // always land the new screen at the top; modern web SPAs
        // mirror that. The three cases:
        //
        //   - 'forward' / 'fade': fresh destination, user wants
        //     to start reading from the top. Reset scroll.
        //   - 'back': the user is RETURNING to a page they were
        //     reading. Browser's history.scrollRestoration =
        //     'auto' (the default) restores the scroll position
        //     from when they left, so we DON'T touch it.
        //   - 'none': first render / same depth refresh. Already
        //     at the top or intentionally not navigating, so no
        //     reset.
        const shouldScrollTop = (dir === 'forward' || dir === 'fade');
        const mySwap = ++swapSeq;
        const swap = () => {
          if (mySwap !== swapSeq) return;   // superseded (see pendingSwapKey)
          pendingSwapKey = null;
          // Its own boundary: under View Transitions this body runs a frame
          // later, so render()'s try/catch has already returned and a throw
          // from the recompute below would escape as an unhandled rejection:
          // the exact vanishing act ADR 0020 exists to end.
          try {
            swapDOM();
          } catch (err) {
            presentPageFailure(err);
          }
        };
        const swapDOM = () => {
          while (root.firstChild) root.removeChild(root.firstChild);
          // Recompute from the CURRENT model, not the `viewVal` snapshot
          // captured at render() entry. startViewTransition runs this
          // callback ASYNCHRONOUSLY (next frame), so a fast init-effect
          // fetch can resolve and advance pg.model BEFORE the swap fires.
          // Mounting the stale snapshot would leave the old "Loading…"
          // view on screen even though the data already arrived: the
          // bug where a freshly-navigated page (e.g. a protected page's
          // dashboard right after sign-in) stuck on "Loading…" until
          // time-travel forced a fresh mount.
          // While time-traveling, mount the frozen frame snapshot (viewVal),
          // not a live recompute: the live model is intentionally paused.
          mounted = createDOM(isTraveling ? viewVal : viewWithUser(pg, pg.model));
          root.appendChild(mounted);
          syncCanvasRootClass();
          // mountedPath tracks the LIVE page (for the on-nav cleanup above);
          // drawnPath tracks what's actually painted. They only diverge while
          // time-travel is showing a past frame from another page.
          mountedPath = pg.activeKey;
          drawnPath = viewPg.activeKey;
          if (shouldScrollTop) {
            // `auto` (not 'smooth') because the page itself is
            // animating in via View Transitions / cross-fade:
            // adding a second animated scroll on top reads as
            // jittery. Instant reset is correct here.
            window.scrollTo({ top: 0, left: 0, behavior: 'auto' });
          }
        };
        if (dir === 'none'
            || !document.startViewTransition
            || prefersReducedMotion()
            || skipNextViewTransition) {
          // Reset the flag: only the immediately-following render
          // should skip; subsequent button-back clicks need their
          // own transition.
          skipNextViewTransition = false;
          swap();
        } else {
          // The data-attribute drives the CSS that picks the right
          // keyframes (forward vs back). We clear it on the
          // transition's `finished` so a subsequent same-depth
          // render doesn't accidentally inherit the previous run's
          // direction.
          document.documentElement.dataset.marNavDir = dir;
          pendingSwapKey = viewPg.activeKey;
          const t = document.startViewTransition(swap);
          t.finished
            .catch(() => { /* user-cancelled / skipped: ignore */ })
            .finally(() => {
              delete document.documentElement.dataset.marNavDir;
            });
        }
      } else {
        // Same page, just an MVU re-render (model changed; URL didn't).
        // Patch in place: no view-transition, no animation. Clear
        // pendingNavKind even though we didn't consume it for direction
        // selection above (the swap branch did), so a stale flag from
        // an early-exit render (e.g. authUser fetch race) doesn't leak
        // into the NEXT swap.
        mounted = patchDOM(mounted, viewVal);
        syncCanvasRootClass();
        pendingNavKind = null;
      }

      // Intercept link clicks pointing at known page paths so navigation
      // stays in-process (no full page reload). One delegated listener on
      // the root catches clicks from any descendant anchor.
      //
      // Hot reload re-runs mountPages which re-runs this block. We
      // remove the previous mount\'s listener before installing a
      // new one (see prevLinkClickHandler comment above) so the
      // root doesn\'t accumulate stale closures that reference
      // dead `pages` / `pushNav` / `render` from old mounts.
      if (!routerInstalled) {
        routerInstalled = true;
        if (prevLinkClickHandler && prevLinkClickRoot) {
          prevLinkClickRoot.removeEventListener('click', prevLinkClickHandler);
        }
        root.addEventListener('click', onLinkClick);
        prevLinkClickHandler = onLinkClick;
        prevLinkClickRoot = root;
      }
    }

    // True when `href` corresponds to a known static page or matches a
    // dynamic pattern. Used by the link interceptor so /notes/abc gets
    // SPA-routed instead of full-reloading.
    function matchesAnyPage(href) {
      if (pages[href]) return true;
      for (const dp of dynamicPages) {
        if (matchPathPattern(href, dp.pattern) !== null) return true;
      }
      return false;
    }

    // pushNav / replaceNav wrap history.pushState / replaceState
    // with the bookkeeping the nav chrome reads back later:
    //
    //   marDepth   - depth counter for the auto back button
    //                (buildNavigationChrome reads currentNavDepth())
    //                and the view-transition direction detector
    //                (render() compares old vs new depth).
    //   prevTitle  - title of the page we\'re LEAVING, captured
    //                from document.title at push time. The back
    //                button on the next page reads this to render
    //                "‹ <where back goes>" instead of a generic
    //                "‹ Back". Mirrors iOS Settings, where the
    //                back chevron is labeled with the previous
    //                screen\'s name.
    //
    // pushNav bumps depth and stamps the leaving page\'s title.
    // replaceNav preserves both (the user didn\'t actually
    // navigate, just changed the URL of the current entry, so
    // the back target stays the same).
    // A navigation supersedes any sheet's queued history cleanup: cancel
    // the deferred history.back() (see syncSheetHistory) and forget the
    // open outlets: navigating unmounts the page that owned those sheets,
    // so their synthetic entries are ours to reuse or discard.
    function adoptPendingSheetEntry() {
      sheetPendingBack = null;
      sheetHistory.open.clear();
    }
    function pushNav(path) {
      const leavingTitle = document.title || '';
      const st = { marDepth: currentNavDepth() + 1, prevTitle: leavingTitle, marEntry: newEntryId() };
      adoptPendingSheetEntry();
      // If a sheet's synthetic entry is the current top (a navigation
      // launched from an open sheet, e.g. "create, then jump to the new
      // record"), OVERWRITE it rather than stack on top: the sheet entry is
      // a "back closes the sheet" marker, not a screen to return to.
      // Replacing keeps the back stack clean (destination → the page under
      // the sheet) and, together with adoptPendingSheetEntry above, stops
      // the sheet's deferred back() from bouncing us off the destination.
      if (history.state && history.state.__marSheet) {
        history.replaceState(st, '', path);
      } else {
        history.pushState(st, '', path);
      }
    }
    function replaceNav(path) {
      const prev = (history.state && history.state.prevTitle) || '';
      adoptPendingSheetEntry();
      // Same url keeps the entry (and its model, as today); another url is
      // another page in this slot.
      let sameUrl = false;
      try { sameUrl = new URL(path, window.location.href).pathname === window.location.pathname; } catch (_) {}
      const keep = sameUrl && history.state && history.state.marEntry;
      history.replaceState(
        { marDepth: currentNavDepth(), prevTitle: prev, marEntry: keep || newEntryId() },
        '',
        path,
      );
    }

    // Expose programmatic navigation so Nav.push / Nav.replace can
    // reach into this closure. Last call wins: successive mountPages
    // (e.g. hot-reload) replace the table.
    globalThis.__marNav = {
      push: (path) => {
        if (refusedFromCovered('Nav.push')) return;
        pendingNavKind = 'push';
        pushNav(path);
        render();
      },
      replace: (path) => {
        if (refusedFromCovered('Nav.replace')) return;
        pendingNavKind = 'replace';
        replaceNav(path);
        // DROPPING THE CACHED USER HERE IS LOAD-BEARING, and it is worth
        // saying why, because it reads like superstition and was very
        // nearly removed as such.
        //
        // It is not really "auth may have changed": it is a refetch
        // before the destination draws. Nulling the cache makes the
        // protected-page gate in renderPage block, fetch /_auth/whoami,
        // and only then render. That is the one thing that makes an edit
        // to the user's OWN record visible: save a handle, replace to the
        // timeline, and the destination sees the handle you just chose.
        //
        // Removing it (on the theory that a bootstrap redirect has nothing
        // to do with auth, so why pay a round trip) turns the mini-twitter
        // signup into a loop: /setup saves, replaces to /, and / re-reads
        // the STALE user, still handle-less, and bounces straight back to
        // /setup. Measured, not imagined.
        //
        // The cost is one round trip per replace. The alternative is an
        // explicit "the user changed" signal from app code, which is a
        // language decision and not a thing to slip in here.
        authUser = null;
        render();
      },
      // replaceFresh: like replace, but also resets marDepth to 0 so
      // the destination has no back-button into the (now-completed)
      // flow that brought us here. Used by Auth.completeSignIn and by the
      // auth-expired redirect: both are entry-point transitions
      // where "going back" makes no sense (you'd land on a page you
      // either can't access yet, or that you just finished with).
      replaceFresh: (path) => {
        if (refusedFromCovered('Auth.completeSignIn')) return;
        pendingNavKind = 'replaceFresh';
        adoptPendingSheetEntry();
        history.replaceState({ marDepth: 0, prevTitle: '', marEntry: newEntryId() }, '', path);
        authUser = null;
        render();
      },
    };

    // Forget who is signed in. Exposed on globalThis because the builtin
    // that needs it (Auth.logout) is built in makeBuiltinEnv, outside this
    // closure: the same reason __marNav is exposed. Logout used to rely on
    // the app happening to follow it with a Nav.replace, which is not a
    // contract, just a habit most sign-out handlers share.
    globalThis.__marAuthForget = () => { authUser = null; };

    // ---------- Time-travel ----------
    //
    // Whole section is gated behind __MAR_DEV__ so esbuild drops it from
    // production. The frame capture in `currentDispatch` (below) and the
    // dock panel registration are gated separately.
    //
    // Hoisted outside the `if (__MAR_DEV__)` block so currentDispatch
    // (defined further down, also inside an `if (__MAR_DEV__)` guard)
    // can see it. In production preservedTimeTravel is null and
    // pushTimeTravelState stays a no-op; the dev guards downstream
    // short-circuit before touching anything.
    const timeTravel = preservedTimeTravel;
    let pushTimeTravelState = () => {};

    // Frame paths are either literal page paths (static pages) or URLs that
    // match a dynamic pattern. Hoisted out of the __MAR_DEV__ block (like the
    // helpers above) so render()'s cross-page time-travel branch can resolve
    // which page a past frame belongs to. Harmless in production (never called
    //: preservedTimeTravel is null so the traveling branch is dead).
    function lookupPageForFrame(framePath) {
      framePath = String(framePath).split('#')[0];
      if (pages[framePath]) return pages[framePath];
      for (const dp of dynamicPages) {
        if (matchPathPattern(framePath, dp.pattern) !== null) {
          return dp.page;
        }
      }
      return null;
    }

    if (__MAR_DEV__) {
    // Each dispatch captures a frame { msg, prevModel, nextModel, hadEffect,
    // pagePath, time } so the time-travel panel can scrub through history.
    // The panel reads `timeTravel.frames` and `timeTravel.cursor`; jumping
    // sets the page's model directly and re-renders. Live dispatch resets
    // travel mode and (if we're not at the end of history) branches:
    // discarding frames after the cursor so the user's new action becomes
    // the new "present".
    //
    // Frames are validated LAZILY (see frameRenders + jumpToFrame): a reload no
    // longer re-renders the whole history: that made reload cost scale with a
    // 60fps app's playtime (thousands of frames, each re-running the full view).
    // A frame is checked only when it's about to be shown: the one under the
    // cursor on reload (if parked in the past), then each frame you scrub to.
    // lookupPageForFrame is hoisted above (out of this __MAR_DEV__ block) so
    // render()'s cross-page time-travel branch can reach it too.

    // Can the current (possibly hot-reloaded) view still draw this frame's
    // snapshots? Both models must render: nextModel is what a forward jump
    // shows, prevModel what a jump BACK through this frame shows. Protected
    // pages before we know the user, and dynamic pages (whose Params only
    // exist for the live URL), are assumed renderable: same as the old pass.
    function frameRenders(frame) {
      const pg = lookupPageForFrame(frame.pagePath);
      if (!pg) return false;
      if (pg.isProtected && getUser() == null) return true;
      if (pg.isDynamic) return true;
      try {
        viewWithUser(pg, frame.nextModel);
        viewWithUser(pg, frame.prevModel);
        return true;
      } catch (_) {
        return false;
      }
    }
    {
      // Lazy: don't re-validate the whole history on reload. Just clamp the
      // cursor into range and re-derive `traveling`. If we reloaded while
      // parked on a PAST frame, render() is about to draw that one frame, so
      // validate just it; if the reloaded view can't render it the recorded
      // history is stale (the model shape changed), so drop it and snap back to
      // the live model rather than crash. Every other frame is validated the
      // moment you scrub to it (jumpToFrame). When not traveling the live model
      // is drawn, never a frame, so stale frames are harmless until scrubbed.
      if (timeTravel.cursor >= timeTravel.frames.length) {
        timeTravel.cursor = timeTravel.frames.length - 1;
      }
      timeTravel.traveling = timeTravel.cursor !== timeTravel.frames.length - 1;
      if (timeTravel.traveling && timeTravel.cursor >= 0 &&
          !frameRenders(timeTravel.frames[timeTravel.cursor])) {
        timeTravel.frames = [];
        timeTravel.cursor = -1;
        timeTravel.traveling = false;
      }
    }

    function jumpToFrame(targetIdx, opts) {
      if (targetIdx < -1 || targetIdx >= timeTravel.frames.length) return;
      if (timeTravel.frames.length === 0) return;
      // Lazy validation: the snapshot we're about to draw must still render
      // under the current (possibly hot-reloaded) view. -1 is the initial state
      // = frame 0's prevModel, so frame 0 covers it. If it no longer renders,
      // the recorded history predates a shape-changing reload: drop it and
      // return to the live model instead of crashing render(). Always refresh
      // the panel here (even mid slider-drag): the slider's range just changed.
      const checkIdx = targetIdx < 0 ? 0 : targetIdx;
      if (!frameRenders(timeTravel.frames[checkIdx])) {
        timeTravel.frames = [];
        timeTravel.cursor = -1;
        timeTravel.traveling = false;
        render();
        pushTimeTravelState();
        return;
      }
      // Read-only: we only move the cursor + traveling flag. We do NOT write
      // pg.model: the live model stays live and keeps advancing. render()
      // draws the frame's snapshot (see drawModel there) while traveling.
      // "Latest" (cursor at the end) means traveling = false → we go back to
      // showing the live model, and clock subs resume.
      timeTravel.cursor = targetIdx;
      timeTravel.traveling = targetIdx !== timeTravel.frames.length - 1;
      render();
      // skipPanelRefresh keeps the dock panel intact during slider drags.
      // Re-rendering the panel rebuilds the <input>, which kills the
      // mouse drag mid-stream: without this guard the user can only
      // step one frame per click instead of scrubbing freely.
      if (!opts || !opts.skipPanelRefresh) {
        pushTimeTravelState();
      }
    }

    pushTimeTravelState = function () {
      const dock = getDevDock();
      dock.updatePanel('time-travel', {
        frames: timeTravel.frames,
        cursor: timeTravel.cursor,
        traveling: timeTravel.traveling,
        total: timeTravel.total,
      });
    };

    // resetTimeTravel re-runs the current page's init function (getting
    // a fresh (model, effect) pair) and wipes the frame history. The
    // resulting init effect is fired so any startup work: HTTP fetch,
    // initial timer, etc.: re-triggers as if the page was just loaded.
    // Multi-page apps: only the current page is reset; siblings keep
    // whatever model they last had.
    function resetTimeTravel() {
      const pg = currentPage();
      if (!pg || !pg.init) return;
      // For protected pages, init takes the User as first arg. Skip
      // reset when we don't have one (the page itself wouldn't render
      // anyway: render() routes to redirect).
      if (pg.isProtected && getUser() == null) return;
      // Apply User then Params (if present), matching the init type
      // signature. applyExtras handles the four flavors uniformly, and
      // what it returns IS the (model, effect) tuple: a plain page
      // declares init as the tuple itself (see initWith).
      const fresh = unwrapModelTuple(applyExtras(pg, pg.init));
      pg.model = fresh.model;
      // A fresh model is a new showing of the page, handed the state whatever
      // it was last handed (pageSubOwner, pageStateSeen).
      subPageKey = null;
      forgetPageStates(pg.activeKey);
      // Allow the init effect to fire again on the next render path:
      // mountPages gates it via initEffectsRun (keyed on activeKey so
      // dynamic pages reset cleanly per-URL), which we toggle off here.
      initEffectsRun[pg.activeKey] = false;
      pg.initEffect = fresh.effect;
      timeTravel.frames = [];
      timeTravel.cursor = -1;
      timeTravel.traveling = false;
      timeTravel.total = 0;
      render(); // render() consumes initEffect (runs it) on the gated branch
      pushTimeTravelState();
    }

    // Dev-only: time-travel panel + frame capture in dispatch. In
    // production (devMode = false on the program JSON) the dock isn't
    // even instantiated; mountPages just runs.
    if (__MAR_DEV__ && global.__marDevMode) {
      const dock = getDevDock();
      dock.registerPanel({
        id: 'time-travel',
        badge: {
          icon: '⏱',
          color: '#93c5fd',
          title: 'Time travel',
          label: (s) => {
            var n = s.total || (s.frames || []).length;   // grows forever, even past the window cap
            return n + ' action' + (n === 1 ? '' : 's');
          },
          visible: (s) => (s.frames || []).length > 0,
          pulse: (s) => !!s.traveling,
        },
        render: renderTimeTravelPanel,
        // Seed with whatever survived hot-reload: registerPanel replaces
        // the panel definition (and state), so without this we'd zero the
        // history visually even though `timeTravel` (the closure variable)
        // still holds the preserved frames.
        initialState: {
          frames: timeTravel.frames,
          cursor: timeTravel.cursor,
          traveling: timeTravel.traveling,
          total: timeTravel.total,
        },
      });
    }

    // After a hot-reload that preserved the model, restore the page's
    // model from the latest frame too. mountPages above already restored
    // the model from preservedScreenModels (which is updated on every
    // dispatch), so this would normally be a no-op, but if the user
    // was time-traveling at reload time, the live model is the frame
    // they had jumped to, and we want the panel state to reflect that.
    if (timeTravel.frames.length > 0) {
      pushTimeTravelState();
    }

    function renderTimeTravelPanel(container, state) {
      const total = state.frames.length;
      if (total === 0) {
        container.textContent = 'No actions yet. Interact with the app to capture a frame.';
        return;
      }
      const cursor = state.cursor;
      // Repaints the model viewer for a given cursor. Declared here (assigned
      // further down, where the viewer is built) so the slider's live-drag
      // handler can call it: the handler only fires after render() completes.
      let paintModel = null;
      // The scrubbable window is `total` frames, but older ones may have been
      // dropped: `dropped` is how many, so displayed numbers match the badge's
      // ever-growing count (frame idx 0 is really action #(dropped+1)).
      const dropped = Math.max(0, (state.total || total) - total);
      const trueTotal = total + dropped;

      // Controls row.
      const controls = document.createElement('div');
      controls.style.display = 'flex';
      controls.style.gap = '6px';
      controls.style.alignItems = 'center';
      controls.style.marginBottom = '8px';

      const mkBtn = (label, title, onclick, disabled) => {
        const b = document.createElement('button');
        b.textContent = label;
        b.title = title;
        b.disabled = !!disabled;
        b.style.background = disabled ? '#374151' : '#374151';
        b.style.color = disabled ? '#6b7280' : '#f3f4f6';
        b.style.border = '1px solid #4b5563';
        b.style.borderRadius = '4px';
        b.style.padding = '2px 8px';
        b.style.cursor = disabled ? 'not-allowed' : 'pointer';
        b.style.fontFamily = 'inherit';
        b.style.fontSize = '12px';
        if (!disabled) b.onclick = onclick;
        return b;
      };

      controls.appendChild(mkBtn('⏮', 'Initial state', () => jumpToFrame(-1), cursor === -1));
      controls.appendChild(mkBtn('◀', 'Previous frame (← / ↓)', () => jumpToFrame(cursor - 1), cursor <= -1));
      controls.appendChild(mkBtn('▶', 'Next frame (→ / ↑)', () => jumpToFrame(cursor + 1), cursor >= total - 1));
      controls.appendChild(mkBtn('⏭', 'Latest', () => jumpToFrame(total - 1), cursor === total - 1));

      // Visual gap before the destructive reset button so users don't
      // confuse it with a navigation control.
      const sep = document.createElement('span');
      sep.style.width = '8px';
      controls.appendChild(sep);

      const resetBtn = mkBtn('↺', 'Reset: restore initial model and clear all frames',
        () => {
          if (confirm('Reset to initial state and clear all ' + total + ' frame' + (total === 1 ? '' : 's') + '?')) {
            resetTimeTravel();
          }
        }, false);
      resetBtn.style.color = '#fca5a5';
      resetBtn.style.borderColor = '#7f1d1d';
      controls.appendChild(resetBtn);

      // Slider. `oninput` fires continuously during drag; we update the
      // app + status text live but skip the full panel re-render so the
      // <input> survives the drag (otherwise the dock would rebuild it
      // mid-mousedown and the user could only step one frame per click).
      // `onchange` fires on release: that's when we sync the panel
      // (including the row-list highlight, traveling banner, etc.).
      const slider = document.createElement('input');
      slider.type = 'range';
      slider.min = '-1';
      slider.max = String(total - 1);
      slider.value = String(cursor);
      slider.className = 'mar-tt-slider';
      slider.style.flex = '1';
      slider.style.minWidth = '120px';
      // Paint the blue "filled" portion of the track up to the cursor
      // (webkit reads --tt-fill in the track gradient). value runs
      // -1..total-1, so (value + 1) / total maps to 0..100%.
      const setFill = (val) => {
        slider.style.setProperty('--tt-fill', ((parseInt(val, 10) + 1) / total * 100) + '%');
      };
      setFill(slider.value);

      const status = document.createElement('span');
      status.textContent = (cursor + 1 + dropped) + ' / ' + trueTotal;
      status.style.color = '#9ca3af';
      status.style.minWidth = '50px';
      status.style.textAlign = 'right';

      slider.oninput = () => {
        const idx = parseInt(slider.value, 10);
        status.textContent = (idx + 1 + dropped) + ' / ' + trueTotal;
        setFill(slider.value);
        jumpToFrame(idx, { skipPanelRefresh: true });
        if (paintModel) paintModel(idx);   // live model update while scrubbing (panel not rebuilt mid-drag)
      };
      slider.onchange = () => {
        jumpToFrame(parseInt(slider.value, 10));
      };

      controls.appendChild(slider);
      controls.appendChild(status);

      container.appendChild(controls);

      {
        // The panel is only rendered while it's the open dock section, and
        // opening it freezes the world (render strips clock subs whenever the
        // panel is open). So the banner shows in BOTH states: scrubbed back
        // vs. sitting at the present, and closing the panel is what resumes.
        const note = document.createElement('div');
        note.textContent = state.traveling
          ? 'Paused — inspecting a past frame (read-only). Close the panel to resume.'
          : 'Paused at the latest frame. Close the panel to resume, or scrub back to inspect.';
        note.style.background = '#1e3a8a';
        note.style.color = '#dbeafe';
        note.style.padding = '4px 8px';
        note.style.borderRadius = '4px';
        note.style.marginBottom = '6px';
        note.style.fontSize = '11px';
        container.appendChild(note);
      }

      // Model viewer: the actual Mar state at the selected frame. We keep every
      // frame's prev/nextModel, so this is the real model you're paused on:
      // cursor -1 shows the initial state (frame 0's prevModel), otherwise the
      // frame's nextModel. Top-level record fields go one-per-line; nested values
      // render inline via displayValue (same formatter the frame rows use).
      const modelAt = (idx) => {
        const f0 = state.frames[0];
        if (idx <= -1) return f0 ? f0.prevModel : null;
        const f = state.frames[idx];
        return f ? f.nextModel : null;
      };
      const modelBox = document.createElement('div');
      modelBox.setAttribute('data-scroll-key', 'model');
      modelBox.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, monospace';
      modelBox.style.fontSize = '11px';
      modelBox.style.lineHeight = '1.5';
      modelBox.style.color = '#e5e7eb';
      modelBox.style.background = '#111827';
      modelBox.style.border = '1px solid #374151';
      modelBox.style.borderRadius = '4px';
      modelBox.style.padding = '6px 8px';
      modelBox.style.flex = '1';        // right column: takes the width the list leaves
      modelBox.style.minHeight = '0';   // so it fills the row height and scrolls internally
      modelBox.style.overflow = 'auto';
      modelBox.style.whiteSpace = 'pre-wrap';
      modelBox.style.wordBreak = 'break-word';
      // paintModel is referenced by slider.oninput above; that handler only runs
      // on a real drag, long after this render defines it, so the forward ref is
      // safe. Keeping it here keeps the whole model section in one place.
      paintModel = (idx) => {
        const m = modelAt(idx);
        modelBox.innerHTML = '';
        if (m && m.k === 'R') {
          (m.order || Object.keys(m.fields || {})).forEach((k) => {
            const line = document.createElement('div');
            const key = document.createElement('span');
            key.textContent = k;
            key.style.color = '#93c5fd';
            const val = document.createElement('span');
            val.textContent = ' = ' + displayValue(m.fields[k], 1);
            line.appendChild(key);
            line.appendChild(val);
            modelBox.appendChild(line);
          });
        } else {
          modelBox.textContent = m == null ? '(no model captured)' : displayValue(m, 0);
        }
      };
      paintModel(cursor);
      // modelBox is placed into the two-column row below (beside the frame list),
      // not appended here.

      // Frame list (newest first). Fills the remaining vertical space
      // inside the panel and is the only scrollable region: controls /
      // banners stay sticky above it. Marked with data-scroll-key so
      // the dock preserves scroll position across re-renders.
      const list = document.createElement('div');
      list.setAttribute('data-scroll-key', 'frames');
      list.style.flex = '1';         // fills its labeled column (basis is on the column wrapper below)
      list.style.minHeight = '0';
      list.style.overflow = 'auto';

      const renderRow = (idx, label, isCursor, hadEffect, numText) => {
        const row = document.createElement('div');
        if (isCursor) row.setAttribute('data-cursor-row', 'true');
        row.style.padding = '4px 6px';
        row.style.cursor = 'pointer';
        row.style.borderBottom = '1px solid #374151';
        row.style.display = 'flex';
        row.style.gap = '8px';
        row.style.alignItems = 'baseline';
        row.style.background = isCursor ? '#1e40af' : 'transparent';
        if (!isCursor) row.onmouseenter = () => (row.style.background = '#374151');
        if (!isCursor) row.onmouseleave = () => (row.style.background = 'transparent');
        const num = document.createElement('span');
        // numText lets a non-action row (the state floor) show a marker instead
        // of a slot number that would collide with a real, dropped action.
        num.textContent = (numText !== undefined ? numText : String(idx + 1 + dropped)).padStart(4, ' ');
        num.style.color = '#9ca3af';
        num.style.fontVariantNumeric = 'tabular-nums';
        const text = document.createElement('span');
        text.textContent = label;
        text.title = label;            // hover shows full label
        text.style.flex = '1';
        text.style.minWidth = '0';     // required for ellipsis inside a flex row
        text.style.color = '#e5e7eb';
        text.style.whiteSpace = 'nowrap';
        text.style.overflow = 'hidden';
        text.style.textOverflow = 'ellipsis';
        row.appendChild(num);
        row.appendChild(text);
        if (hadEffect) {
          const eff = document.createElement('span');
          eff.textContent = '⚠';
          eff.title = 'This frame triggered effects that won\'t be undone by time-travel.';
          eff.style.color = '#fbbf24';
          row.appendChild(eff);
        }
        row.onclick = () => jumpToFrame(idx);
        return row;
      };

      for (let i = state.frames.length - 1; i >= 0; i--) {
        const f = state.frames[i];
        list.appendChild(renderRow(i, displayValue(f.msg), i === cursor, f.hadEffect));
      }
      // Bottom row: the earliest state we can still reach. When nothing has
      // been dropped it's the true initial state (numbered 0). When frames
      // HAVE been dropped, this row is NOT a real action: action #dropped and
      // everything before it were evicted, so their msgs (Tick? Tapped?) are
      // gone. Show a '⋯' marker instead of a slot number so it reads as a state
      // floor, not "action #dropped = (oldest kept state)".
      if (dropped > 0) {
        list.appendChild(renderRow(-1, 'oldest kept state · ' + dropped + ' earlier dropped', cursor === -1, false, '⋯'));
      } else {
        list.appendChild(renderRow(-1, '(initial state)', cursor === -1, false));
      }
      // Two columns filling the remaining height: frame timeline (left) and the
      // model inspector (right). Each carries a small header so it's clear what
      // you're looking at: especially that the right side IS the page's model.
      // The panel widens for time-travel to fit both.
      const labeledColumn = (basis, label, body) => {
        const col = document.createElement('div');
        col.style.display = 'flex';
        col.style.flexDirection = 'column';
        col.style.flex = basis;
        col.style.minWidth = '0';
        col.style.minHeight = '0';
        const hdr = document.createElement('div');
        hdr.textContent = label;
        hdr.style.flex = '0 0 auto';
        hdr.style.color = '#9ca3af';
        hdr.style.fontSize = '10px';
        hdr.style.fontWeight = '600';
        hdr.style.textTransform = 'uppercase';
        hdr.style.letterSpacing = '0.6px';
        hdr.style.padding = '0 0 4px';
        col.appendChild(hdr);
        col.appendChild(body);
        return col;
      };
      const cols = document.createElement('div');
      cols.style.display = 'flex';
      cols.style.flex = '1';
      cols.style.minHeight = '0';
      cols.style.gap = '10px';
      cols.style.borderTop = '1px solid #374151';
      cols.style.paddingTop = '6px';
      cols.appendChild(labeledColumn('0 0 42%', 'frames', list));
      cols.appendChild(labeledColumn('1', 'model', modelBox));
      container.appendChild(cols);

      // Scroll the cursor row into view if it's outside the list's
      // visible area. `block: 'nearest'` is the right setting, it only
      // scrolls when needed (no jump if already visible) and picks the
      // shortest path (top vs bottom edge). Deferred via queueMicrotask
      // so it runs after the dock's scroll-position restore step,
      // overriding the saved position only when truly necessary.
      const queue = (typeof queueMicrotask === 'function')
        ? queueMicrotask
        : (fn) => setTimeout(fn, 0);
      queue(() => {
        const cursorRow = list.querySelector('[data-cursor-row="true"]');
        if (cursorRow && typeof cursorRow.scrollIntoView === 'function') {
          cursorRow.scrollIntoView({ block: 'nearest' });
        }
      });
    }

    // Wire the module-level keyboard handler (registered once per page
    // load up at the top of the IIFE) into THIS mountPages's jumpToFrame
    // closure. Each hot-reload re-runs mountPages and re-points to the
    // fresh closure; the listener itself is never re-registered, so
    // arrow keys don't multiply.
    currentJumpToFrame = jumpToFrame;
    currentDevRerender = render;
    } // end if (__MAR_DEV__): time-travel block

    // ── Runtime failures ──────────────────────────────────────────────
    //
    // ADR 0020. Every runtime error a checked program can still raise is a
    // bug: runaway recursion, a `case` over literals with no catch-all, or an
    // integer leaving the 53-bit range. All three are deterministic: the same
    // input fails the same way, so the message never offers a retry the way
    // `Service.errorToString` does, where the network really does come back.
    //
    // What differs between the three screens below is only whether anything is
    // left to stand on.
    const RUNTIME_ERROR_TITLE = 'This app has a critical bug.';

    // 'dispatch', update, a tagger, or an effect. `update` never mutates:
    //   it returns a new model or it throws, so a throw means the model was
    //   never replaced and the screen behind this message is a consistent one.
    // 'page'     - view or init, with an entry to go back to.
    // 'stuck'    - view or init on the first screen. Nothing to return to, and
    //   reloading re-runs the init that just failed, so nothing is offered.
    function runtimeErrorBody(kind) {
      if (kind === 'dispatch') {
        return ['Something unexpected happened and your request could not be completed.',
                'Nothing was changed. The app is back at its last consistent state.'];
      }
      return ['Something unexpected happened and this screen could not be shown.',
              kind === 'page'
                ? 'Go back to return the app to its last consistent state.'
                : 'The app cannot continue until a developer fixes it.'];
    }

    function runtimeErrorText(err) {
      return (err && err.message) || String(err);
    }

    // The message carries its own button rather than leaning on the navigation
    // bar: the bar's title and toolbar come out of the same `view` call that
    // just threw, so on a fresh navigation there is no chrome to keep, and
    // keeping the previous page's would show the wrong title.
    function runtimeErrorNode(kind, site, err) {
      const box = document.createElement('div');
      box.className = 'mar-runtime-error mar-runtime-error-' + kind;
      box.setAttribute('role', 'alert');

      if (__MAR_DEV__) {
        // Development gets the failure itself. There is no file:line:column
        // to add: the serialized AST carries no positions (see
        // internal/jsserve/serialize.go), so the browser does not know where
        // the expression came from and inventing one would be worse than
        // omitting it.
        const head = document.createElement('p');
        head.className = 'mar-runtime-error-title';
        head.textContent = site + ' failed';
        box.appendChild(head);
        const detail = document.createElement('pre');
        detail.className = 'mar-runtime-error-detail';
        detail.textContent = runtimeErrorText(err);
        box.appendChild(detail);
        return box;
      }

      const head = document.createElement('p');
      head.className = 'mar-runtime-error-title';
      head.textContent = RUNTIME_ERROR_TITLE;
      box.appendChild(head);
      for (const line of runtimeErrorBody(kind)) {
        const p = document.createElement('p');
        p.className = 'mar-runtime-error-line';
        p.textContent = line;
        box.appendChild(p);
      }
      if (kind === 'page') {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'mar-runtime-error-back';
        btn.textContent = 'Go back';
        btn.addEventListener('click', () => history.back());
        box.appendChild(btn);
      }
      return box;
    }

    // The banner lives outside #mar-root so the next render doesn't wipe it,
    // and it stays until a dispatch succeeds: nothing times it out, because a
    // bug does not heal on its own.
    function clearDispatchError() {
      if (dispatchErrorBanner && dispatchErrorBanner.parentNode) {
        dispatchErrorBanner.parentNode.removeChild(dispatchErrorBanner);
      }
      dispatchErrorBanner = null;
    }

    function reportRuntimeError(site, err) {
      // The stylesheet is installed by the renderers, one per view tag, so a
      // failure early enough that no view ever rendered would otherwise draw
      // this message as unstyled black text on white. Ask for it here: it is
      // idempotent, and this is the one screen that cannot rely on a view
      // having been drawn first.
      ensureUIStyles();
      const message = runtimeErrorText(err);
      global.__marLastError = site + ' failed: ' + message;
      if (typeof console !== 'undefined' && console.error) {
        console.error('[mar] ' + site + ' failed: ' + message, err);
      }
      clearDispatchError();
      dispatchErrorBanner = runtimeErrorNode('dispatch', site, err);
      document.body.appendChild(dispatchErrorBanner);
    }

    // A failed `view` or `init` has no screen to preserve, so the message takes
    // the page. Deduplicated by text: a game whose view throws fails again
    // every frame, and rebuilding identical DOM 60 times a second would be
    // both wasteful and, since it replaces what you are reading, a flicker.
    function presentPageFailure(err) {
      // Same reason as reportRuntimeError: a cold load whose `init` or `view`
      // throws never reaches a renderer, so nothing has installed the CSS.
      ensureUIStyles();
      const site = 'view';
      const message = runtimeErrorText(err);
      global.__marLastError = site + ' failed: ' + message;
      if (typeof console !== 'undefined' && console.error) {
        console.error('[mar] ' + site + ' failed: ' + message, err);
      }
      // Only offer Back when there is somewhere to go: a cold load, a deep
      // link or a reload starts at depth 0 with an empty stack behind it.
      const kind = currentNavDepth() > 0 ? 'page' : 'stuck';
      const signature = kind + ' ' + message;
      if (pageFailure === signature) return;
      pageFailure = signature;
      clearDispatchError();
      const root = document.getElementById('mar-root');
      if (!root) return;
      while (root.firstChild) root.removeChild(root.firstChild);
      root.appendChild(runtimeErrorNode(kind, site, err));
      // The DOM the reconciler believes is on screen is gone. Leaving `mounted`
      // pointing at it would make the next successful render try to patch a
      // detached tree instead of mounting fresh.
      mounted = null;
      drawnPath = null;

      // STOP. `view` is a pure function of the model, so the same model will
      // fail the same way on every future frame: there is no state the program
      // can reach on its own where it starts working again. Left running, a
      // game whose view throws keeps ticking sixty times a second behind a dead
      // screen: a laptop left overnight burned eleven hours of battery and
      // recorded 2.4 million actions nobody would ever see.
      //
      // Only the subscriptions are cut, and only after the message is painted:
      // that is what makes the app quiet without taking anything away from the
      // person debugging it. The dev dock keeps its history, so time travel
      // still scrubs back to the frame before the failure, and a hot reload
      // remounts and clears the halt.
      halted = true;
      teardownAllSubs();
    }

    currentDispatch = (msg) => {
      // A halted program accepts no more messages. Subscriptions are already
      // torn down; this catches the in-flight ones: an effect that resolves
      // after the failure, a DOM handler on the dead screen, so the model
      // cannot advance past the state the failure is being reported about.
      if (halted) return;
      const pg = currentPage();
      if (!pg) return;

      const prevModel = pg.model;
      const issuer = issuerFor(pg);
      // Frame-time probe (gated by ?perf): stamp the start of the work so we
      // can measure update + render below. Zero cost when the flag is off.
      const __t0 = (__PERF_ON && typeof performance !== 'undefined') ? performance.now() : 0;
      let out;
      try {
        out = unwrapModelTuple(updateWithUser(pg, msg, prevModel));
      } catch (err) {
        if (err && err.marNoCaseBranch) {
          // A stale message: an async effect (a Service.call, Cmd.perform,
          // or subscription) started by a PREVIOUS page resolved AFTER we
          // navigated away, so `currentPage()` is no longer the page that
          // issued it. Compile-time case exhaustiveness guarantees a page
          // can always match its OWN messages, so a no-branch failure here
          // can only be a message meant for a torn-down page. Drop it: it
          // isn't the live page's concern: rather than crash the whole app
          // with an unhandled rejection.
          if (__MAR_DEV__ && global.__marDevMode && typeof console !== 'undefined' && console.warn) {
            console.warn('[mar] ignored a message delivered after navigation (a stale async result from a previous page): ' + describeValue(msg));
          }
          return;
        }
        // Anything else is a real runtime failure inside `update`. Rethrowing
        // here made it an unhandled rejection: the last render stayed on
        // screen, the tap looked like it simply did nothing, and the only
        // trace was a console entry nobody has open. The model is left
        // untouched (the update never completed), which mirrors the iOS
        // runtime: see MarPageRuntime.dispatch.
        reportRuntimeError('update', err);
        return;
      }
      // A dispatch that completed is the app working again, which is the only
      // honest reason to take the message down.
      clearDispatchError();
      pg.model = out.model;
      // model setter already writes to preservedScreenModels[activeKey];
      // no separate write needed here.

      // Frame capture is dev-only: no need to keep the history around in
      // production (and it would just leak memory).
      if (__MAR_DEV__ && global.__marDevMode) {
        const hadEffect = !!(out.effect && out.effect.k === 'E' && out.effect.tag !== 'none');
        // Append only, never branch. Inspecting a past frame is read-only, so
        // a new live msg (an async result; clock ticks are paused while
        // traveling) just goes on the end. It never rewrites history or the
        // model the user is looking at.
        recordTimeTravelFrame({
          msg, prevModel, nextModel: out.model, hadEffect,
          // pagePath records activeKey (route and history entry) so a frame
          // renders against the right page.
          pagePath: pg.activeKey, time: Date.now(),
        });
      }

      // Intermediate catch-up ticks (ADR-0003) advance the model without
      // painting; the burst's final tick arrives with tickBurst off and
      // renders the frame once. Skipping render also defers sub reconcile
      // to the burst end: render() is the reconcile funnel, and the last
      // tick's model is the one that matters.
      if (!tickBurst) render();
      // The measured window is update + render (the view rebuild + DOM
      // reconcile): the interpreter cost we optimize. The effect runs after,
      // outside the measure, since Cmd.none / sound scheduling isn't the
      // per-frame bottleneck. Suppressed burst ticks fold their update time
      // into the next painted dispatch so the readout stays ms per PAINTED
      // frame.
      if (__t0) {
        const el = performance.now() - __t0;
        if (tickBurst) { tickBurstPerfCarry += el; }
        else { perfRecord(tickBurstPerfCarry + el); tickBurstPerfCarry = 0; }
      }
      runEffectAs(issuer, out.effect);
    };

    function recordTimeTravelFrame(frame) {
      timeTravel.total = timeTravel.total + 1;   // the badge counter: grows forever
      timeTravel.frames.push(frame);
      const TT_MAX_FRAMES = 10000;
      // The counter keeps climbing, but the SCRUBBABLE window is the last
      // TT_MAX_FRAMES: drop older frames (you just can't rewind past them).
      // Trim + follow the tail ONLY when live; while inspecting a past frame
      // we drop nothing (the frame under the cursor must never be evicted)
      // and leave the cursor parked. Both resume when the user goes live.
      if (!timeTravel.traveling) {
        if (timeTravel.frames.length > TT_MAX_FRAMES) {
          timeTravel.frames = timeTravel.frames.slice(-TT_MAX_FRAMES);
        }
        timeTravel.cursor = timeTravel.frames.length - 1;
      }
      pushTimeTravelState();
    }

    // The issuer of a page's effects: its stack entry, and the key its model
    // lives under (route plus entry). Captured when the effect runs, so a
    // result finds the page that asked for it wherever the person has gone.
    function issuerFor(pg) {
      return { key: pg.activeKey, entryId: curEntryId(), params: pg.params };
    }
    // Brings liveEntries up to date with the entry being shown: the entries at
    // its depth or deeper, other than itself, have left the stack (Back popped
    // them, or a push replaced what was above). Their models are freed and
    // their ids retired.
    function syncLiveStack(id, depth, key) {
      for (const [eid, e] of [...liveEntries]) {
        if (eid === id) { if (e.key !== key) dropKey(e.key); continue; }
        if (e.depth >= depth) { liveEntries.delete(eid); retiredEntries.add(eid); dropKey(e.key); }
      }
      liveEntries.set(id, { depth, key });
    }
    function dropKey(key) {
      const p = lookupPageForFrame(key);
      if (p) p.resetKey(key);
      pendingPageErrors.delete(key);
    }
    issuerIsCovered = (issuer) => {
      if (!issuer || !issuer.key) return false;
      const top = currentPage();
      return !(top && top.activeKey === issuer.key);
    };
    pageDeliverHook = (issuer, msg) => {
      if (halted) return;
      const live = liveEntries.get(issuer.entryId);
      if (!live || live.key !== issuer.key) {
        if (__MAR_DEV__ && global.__marDevMode && typeof console !== 'undefined' && console.warn) {
          console.warn('[mar] dropped a result for a page that left the stack: ' + describeValue(msg));
        }
        return;
      }
      const top = currentPage();
      if (top && top.activeKey === issuer.key) { if (currentDispatch) currentDispatch(msg); return; }
      // Covered: run its update on the model its entry keeps.
      const cpg = lookupPageForFrame(issuer.key);
      if (!cpg) return;
      const prevModel = cpg.modelAt(issuer.key);
      if (prevModel === undefined) return;
      // A protected page needs its person. Not known yet (a replace or a
      // reload forgets the cached answer): ask, then deliver. Signed out:
      // there is nobody to update for.
      if (cpg.isProtected && authUser == null) {
        fetchAuthMe().then(() => pageDeliverHook(issuer, msg));
        return;
      }
      const user = getUser();
      if (cpg.isProtected && user == null) return;
      let out;
      try {
        let f = cpg.update;
        if (cpg.isProtected) f = apply(f, user);
        if (cpg.isDynamic) f = apply(f, issuer.params);
        out = unwrapModelTuple(apply(apply(f, msg), prevModel));
      } catch (err) {
        if (err && err.marNoCaseBranch) return;   // see currentDispatch
        if (!pendingPageErrors.has(issuer.key)) pendingPageErrors.set(issuer.key, err);
        return;
      }
      cpg.setModelAt(issuer.key, out.model);
      // An update that worked clears a failure held from an earlier one, as
      // a dispatch that works clears the banner on screen.
      pendingPageErrors.delete(issuer.key);
      if (__MAR_DEV__ && global.__marDevMode) {
        const hadEffect = !!(out.effect && out.effect.k === 'E' && out.effect.tag !== 'none');
        recordTimeTravelFrame({ msg, prevModel, nextModel: out.model, hadEffect,
          pagePath: issuer.key, offscreen: true, time: Date.now() });
      }
      // Not repainted, even under a presented route where it shows: a page
      // that is not on top does not change the screen. Its view could open
      // a UI.sheet or a confirm whose history entry and messages would land
      // on the route above it. It shows its model when it is on top again.
      runEffectAs(issuer, out.effect);
    };

    // Stamp marDepth: 0 onto the initial history entry so every
    // subsequent push/replace can read + bump it. Without this, the
    // first navigation sees `history.state == null`, treats depth as
    // 0, pushes with depth 1, which is correct but only by accident.
    // Doing it explicitly here keeps `currentNavDepth()` honest from
    // the very first render onward.
    if (history.state == null || typeof history.state.marDepth !== 'number' || !history.state.marEntry) {
      replaceNav(window.location.pathname + window.location.search + window.location.hash);
    }

    // A presented route opened cold, a bookmark, a shared link, a reload:
    // used to render as a full screen, because there was nothing behind it to
    // present over. That was worse than merely odd: `Nav.dismiss` is a no-op
    // on the first history entry, so the sheet's own Done button did nothing
    // and the screen was a dead end.
    //
    // So give it something behind. The rule needs no new API because a
    // presented route already nests in the URL under the screen it covers:
    // /classes/3/attendance sits under /classes/3, and being a prefix of a
    // CONCRETE url means the parent's params are already filled in. Seeding
    // the two-entry stack here, rather than special-casing the render, means
    // everything downstream (the presenting branch, dismiss, Back) works with
    // no carve-out at all.
    //
    // It boots AT the parent and then navigates to the sheet, rather than
    // teaching render() a third case. The presenting branch already requires
    // something mounted behind the overlay, and on a cold start nothing is:
    // so instead of relaxing that requirement, this replays the two steps the
    // seeded history claims already happened. render() learns nothing new.
    let coldPresentation = null;
    (function seedColdPresentation() {
      const boot = currentPage();
      if (!boot || !boot.isSheet || currentNavDepth() !== 0) return;
      const covered = coveredPathFor(window.location.pathname);
      if (!covered) return;
      coldPresentation = window.location.pathname + window.location.search + window.location.hash;
      replaceNav(covered);
    })();

    // Replace the previous popstate listener (from the prior
    // mountPages, if any) with this mount\'s render. See the
    // `prevPopstateHandler` declaration above for why this matters
    // on hot reload.
    //
    // We wrap render() in a shim that inspects the PopStateEvent's
    // `hasUAVisualTransition` flag: set to true by Safari (and
    // other UAs) when a back/forward traversal triggered by a
    // platform gesture (two-finger swipe-back on macOS Safari) is
    // ALREADY being visually transitioned by the browser. Layering
    // our own startViewTransition on top of that produces a
    // visible double-animation. The shim suppresses our transition
    // for exactly that one render.
    const handlePopState = (ev) => {
      if (ev && ev.hasUAVisualTransition) {
        skipNextViewTransition = true;
      }
      render();
    };
    if (prevPopstateHandler) {
      window.removeEventListener('popstate', prevPopstateHandler);
    }
    prevPopstateHandler = handlePopState;
    window.addEventListener('popstate', handlePopState);
    // A shared model change repaints the current page: its builder is
    // re-applied with the new value, so its VIEW moved even though its own
    // model did not.
    sharedRerender = render;
    render();
    // The covered screen is mounted now, so the sheet has something to be
    // presented over. This is an ordinary push as far as the runtime is
    // concerned, which is the point.
    if (coldPresentation !== null) {
      pushNav(coldPresentation);
      render();
    }
    // The stores' init Cmds run only now. init happens while pages are being
    // built, before there is a dispatch to deliver a result to, so a
    // Service.call fired there would resolve into nothing.
    const cmds = pendingSharedCmds;
    pendingSharedCmds = [];
    for (const c of cmds) runEffectAs({ shared: c.def }, c.cmd);
    return VUnit();
  }

  // ---------- Module loader ----------

  function loadModule(env, mod, modulesByName) {
    const modName = (mod.name || []).join('.');
    // Per-module env scoping. The synthetic `__entry` module
    // (apphost.PickFrontMods appends it with `name: nil`) is special-
    // cased: its `__entry` value has to live in the shared env so
    // marRun's `envLookup(env, program.entry)` finds it. Real modules
    // get their own frame so two modules that both declare a bare
    // `page` / `renderBody` / `projectsSection` (and bigapp has all
    // three) don't clobber each other in the shared bindings: the
    // last-write-wins on a shared env makes the result depend on the
    // non-deterministic topo iteration order, which is why this bug
    // was intermittent. Closures captured in modEnv chain to the
    // shared env via parent so cross-module qualified lookups still
    // succeed.
    const modEnv = modName ? envNew(env) : env;

    // Pass 0: process `import M exposing (...)` so bare names bind to
    // already-known qualified values. Mirrors the typechecker; without
    // this, code that typechecks (e.g. `column [...]` after
    // `import View exposing (column)`) explodes at runtime with
    // "unbound name: column".
    if (mod.imports) {
      for (const imp of mod.imports) {
        if ((!imp.exposing || imp.exposing.length === 0) && !imp.all) continue;
        const impName = (imp.module || []).join('.');
        // `exposing (..)`: bind every export of the module bare:
        // values and ctors registered as `impName.x` in the env chain
        // (for builtin modules like UI, the whole vocabulary). Mirrors
        // the typechecker's wildcard handling.
        if (imp.all) {
          const exports = envExportsOf(modEnv, impName);
          for (const name in exports) envDefine(modEnv, name, exports[name]);
        }
        if (!imp.exposing) continue;
        for (const item of imp.exposing) {
          const qualified = impName + '.' + item.name;
          const v = envLookup(modEnv, qualified);
          if (v !== undefined) {
            envDefine(modEnv, item.name, v);
          }
          // `Type(..)`: pull every constructor of the imported type
          // into the bare namespace too. The imported module's AST is
          // the source of truth for the ctor list.
          if (item.open && modulesByName) {
            const impMod = modulesByName[impName];
            if (impMod && impMod.decls) {
              for (const d of impMod.decls) {
                if (d.kind !== 'CustomTypeDecl' || d.name !== item.name) continue;
                for (const c of d.constructors) {
                  const cv = envLookup(modEnv, impName + '.' + c.name);
                  if (cv !== undefined) envDefine(modEnv, c.name, cv);
                }
              }
            }
          }
        }
      }
    }
    // Pass 1: register custom-type constructors. Bare goes into
    // modEnv (module-local); qualified `Module.Ctor` goes into the
    // shared env so `import M exposing (T(..))` from other modules
    // (Pass 0 above) can find it.
    for (const d of mod.decls) {
      if (d.kind === 'CustomTypeDecl') {
        const ctorNames = [];
        let allZeroArg = true;
        for (const c of d.constructors) {
          const arity = c.argCount;
          const ctor = arity === 0
            ? VCtor(c.name)
            : native(arity, args => VCtor(c.name, args));
          envDefine(modEnv, c.name, ctor);
          if (modName) envDefine(env, modName + '.' + c.name, ctor);
          ctorNames.push(c.name);
          if (arity > 0) allZeroArg = false;
        }
        // Path-pattern enum registry: a `{role:Role}` segment in
        // some Page.dynamic looks up `Role` here to know the URL ↔
        // ctor mapping. Mirrors the Go runtime's RegisterEnumType.
        // Only zero-arg-ctor types qualify; types with payload are
        // rejected by the typechecker for path use, but the filter
        // here is defensive.
        if (allZeroArg) {
          enumTypes[d.name] = ctorNames;
          if (modName) enumTypes[modName + '.' + d.name] = ctorNames;
        }
      }
    }
    // Pass 2: pre-bind value names with placeholders. Module-local:
    // only this module's body needs the self-reference; other modules
    // reach the value via its qualified alias once Pass 3 runs.
    for (const d of mod.decls) {
      if (d.kind === 'ValueDecl') {
        envDefine(modEnv, d.name, VUnit());
      }
    }
    // Pass 3: evaluate. Bodies resolve bare names against modEnv
    // (shadowing but chaining to env); closures captured here keep
    // modEnv as their lexical env, so calls from other modules still
    // see this module's own bare bindings.
    for (const d of mod.decls) {
      if (d.kind !== 'ValueDecl') continue;
      let body = d.body;
      if (d.params && d.params.length > 0) {
        body = { kind: 'ELambda', params: d.params, body };
      }
      let val = evalExpr(body, modEnv);
      envDefine(modEnv, d.name, val);
      if (modName) envDefine(env, modName + '.' + d.name, val);
    }
  }

  // ---------- Public entry ----------

  // A program.json that is not a program says so, in the same sentences the
  // phone uses.
  //
  // The web used to walk straight into loading it and fail with
  // `entry not found: main` — for a document with no modules at all, or for
  // one that is not an object. Measured against the Swift decoder, which
  // answers `program.json is not an object` and
  // `program.json missing modules`:
  //
  //   42, {}, {modules: "x"}   web: entry not found: main
  //   {modules:[{name:...}]}   web: mod.decls is not iterable
  //
  // It arrives over the network — a truncated download, a proxy answering
  // with HTML, a stale cache — and "entry not found: main" sends someone
  // hunting for a `main` that is right there.
  function checkProgramShape(program) {
    if (program === null || typeof program !== 'object' || Array.isArray(program)) {
      throw new Error('program.json is not an object');
    }
    const mods = program.modules !== undefined ? program.modules : program.module;
    if (mods === undefined) {
      throw new Error('program.json missing `modules` (or legacy `module`)');
    }
    const list = Array.isArray(mods) ? mods : [mods];
    for (const m of list) {
      if (m === null || typeof m !== 'object' || !Array.isArray(m.decls)) {
        throw new Error('program.json: a module has no `decls`');
      }
    }
    for (const m of list) checkNodeKinds(m.decls);
  }

  // The expression and pattern kinds the evaluator has arms for.
  //
  // Written out because JS cannot read its own switch. It is not a second
  // source of truth: internal/parity/node_coverage_test.go compares this
  // list with the `case` arms below AND with the node types in internal/ast,
  // so a node added to the language without an arm here fails the build.
  const KNOWN_NODE_KINDS = new Set([
    'EApp', 'EBinop', 'ECase', 'EChar', 'ECtor', 'EDecimal', 'EFieldAccess',
    'EFieldAccessor', 'EIf', 'EInt', 'ELambda', 'ELet', 'EList', 'ENegate',
    'EQualified', 'ERecord', 'ERecordUpdate', 'EString', 'ETuple', 'EUnit', 'EVar',
    'PChar', 'PCons', 'PCtor', 'PInt', 'PList', 'PRecord', 'PString', 'PTuple',
    'PUnit', 'PVar', 'PWildcard',
  ]);

  // checkNodeKinds refuses a program carrying a node this runtime cannot
  // evaluate, at LOAD, before any of it runs.
  //
  // The kind is only discovered by `evalExpr` otherwise, which is whenever
  // that expression is first reached: a page three navigations in, a branch
  // taken once a week, or never. So a program the runtime cannot fully run
  // would run PARTLY, and the failure would arrive somewhere with no
  // connection to its cause. The Swift runtime already answers this at decode
  // ("unknown expr kind"), because building its enum forces the question; the
  // web never had to ask it.
  //
  // The reachable cause is version skew — a cached runtime.js meeting a newer
  // program.json — which this project has already seen from the other side
  // (`marRun is not defined`). Measured at 4.5ms for the largest example
  // (70k nodes, 2.6MB), against a JSON.parse of the same bytes that costs
  // more.
  function checkNodeKinds(node) {
    if (Array.isArray(node)) {
      for (const child of node) checkNodeKinds(child);
      return;
    }
    if (node === null || typeof node !== 'object') return;
    const kind = node.kind;
    if (typeof kind === 'string' && (kind[0] === 'E' || kind[0] === 'P') && !KNOWN_NODE_KINDS.has(kind)) {
      throw new Error('program.json: unknown node kind `' + kind + '`. ' +
        'This program was built by a newer Mar than the runtime loading it.');
    }
    for (const key in node) checkNodeKinds(node[key]);
  }

  global.marRun = function (program) {
    checkProgramShape(program);
    // devMode is sticky on the global so mountPages, setupDevChannel,
    // and the time-travel capture path all check the same source. The
    // server stamps it into program.json (see makeProgramJSON in
    // internal/jsserve/livereload.go and internal/scaffold/build.go).
    global.__marDevMode = !!program.devMode;
    // Auth metadata baked in by the server. Main.mar isn't in the
    // browser bundle (only page-reachable modules are), so the
    // server hands resolved auth config: currently signInPath for
    // Page.protected: through this side channel.
    if (program.auth && typeof program.auth.signInPath === 'string') {
      globalThis.__marAuthSignInPath = program.auth.signInPath;
    }
    // Only a program that makes sound gets the audio-unlock listeners.
    // Arming them unconditionally opened an AudioContext on the first
    // click of EVERY app, which is what put the browser's "playing audio"
    // speaker on the tab of apps that never make a noise.
    if (referencesSound(program.modules || program.module)) armAudioUnlock();
    const env = makeBuiltinEnv();
    // Modern wire format: a list of modules, loaded in dependency
    // order (the server topo-sorted them). Each module's decls get
    // bound bare (for intra-module references) AND under a qualified
    // `Module.name` alias (so EQualified lookups across modules
    // resolve). Backward-compat: older bundles sent a single `module`.
    const modules = Array.isArray(program.modules)
      ? program.modules
      : (program.module ? [program.module] : []);
    // Index modules by dotted name so loadModule's Pass 0 can resolve
    // `import M exposing (Type(..))` against M's AST (we need M's
    // constructor list to expose them all as bare).
    const modulesByName = Object.create(null);
    for (const m of modules) {
      const name = (m.name || []).join('.');
      if (name) modulesByName[name] = m;
    }
    for (const mod of modules) {
      loadModule(env, mod, modulesByName);
    }
    const main = envLookup(env, program.entry || 'main');
    if (main === undefined) {
      throw new Error('entry not found: ' + (program.entry || 'main'));
    }
    if (main.k !== 'E') {
      throw new Error('entry value is not an Effect (got ' + main.k + ')');
    }
    main.run();
  };

  // marReload tears down the currently running app and re-mounts a fresh
  // one from the latest program.json. Called by the SSE reload listener.
  // Tearing down means: stop dispatching (so any listeners still attached
  // to old DOM nodes become no-ops) and clear the mount root. The old
  // mountApp / mountScreens closures become unreachable garbage.
  global.marReload = function () {
    currentDispatch = null;
    // The def objects die with the old program, so the stores keyed by them
    // are dead too. Their MODELS live on in preservedSharedModels, which is
    // keyed by declaration order rather than by object: a save should not
    // sign you out or refetch your profile, exactly as it does not reset a
    // page model.
    sharedStores.clear();
    pendingPageErrors.clear();
    sharedRerender = null;
    pendingSharedCmds = [];
    sharedDefSeq = 0;
    const root = document.getElementById('mar-root');
    if (root) while (root.firstChild) root.removeChild(root.firstChild);
    return fetch('/_mar/program.json', { cache: 'no-store' })
      .then(readProgram)
      .then(function (p) { global.marRun(p); })
      .catch(bootFailed);
  };

  // readProgram turns a program.json response into the program, and a
  // non-2xx one into an error that names the status, rather than letting
  // r.json() fail on an error page as "Unexpected token".
  function readProgram(r) {
    if (!r.ok) throw new Error('/_mar/program.json answered HTTP ' + r.status);
    return r.json();
  }

  // bootFailed reports a program that could not be started: fetched badly,
  // refused by checkProgramShape, missing its entry, or throwing while main
  // ran. The screen itself is the shell's (BootFailureHead in
  // internal/jsserve/bootfail.go), because it also has to cover runtime.js
  // never running, which nothing in this file can report. Every page Mar
  // serves defines it; a page that does not is not a Mar shell, and gets
  // the console.
  function bootFailed(e) {
    if (typeof global.__marBootFailed === 'function') global.__marBootFailed(e);
    else console.error('[mar] the app could not start:', e);
  }

  // marBootstrap is the entry point invoked from the host HTML page.
  // It fetches the program, runs it, and (in dev) opens the SSE
  // connection for hot reload + compile-error events.
  //
  // The HTML head includes <link rel="preload" href="/_mar/program
  // .json" as="fetch">, so this fetch is typically a cache hit on the
  // browser's preload buffer: the actual download started in parallel
  // with runtime.js as soon as the head was parsed. Total wait is
  // max(T_runtime_load, T_program_download), not their sum.
  //
  // No `cache: 'no-store'` here even though the response is meant to
  // be fresh on every cold start. The server already pins
  // `Cache-Control: no-store` on the response (see
  // /_mar/program.json handler in server.go), so the browser won't
  // persist it between page loads. Setting `cache: 'no-store'` on
  // the fetch *additionally* would make Chrome refuse to use the
  // preloaded entry (the cache modes have to match), which manifests
  // as the warning "preloaded using link preload but not used within
  // a few seconds", and the second network round-trip defeats the
  // whole point of the preload.
  global.marBootstrap = function () {
    // Reuse the eager fetch the HTML shell started (see server.go's
    // inline <script> in the <head>) so we don't issue a second
    // network round-trip. Fall back to a fresh fetch when the shell
    // didn't provide one (e.g. when the runtime is mounted by a
    // non-mar host page like the static `dist/` build).
    var pending = global.__marProgramPromise
      || fetch('/_mar/program.json').then(readProgram);
    // Both outcomes are handled: a program.json that failed ends on the
    // boot failure screen, never on the loading placeholder.
    pending.then(function (p) {
      try { global.marRun(p); }
      catch (e) { bootFailed(e); }
      if (__MAR_DEV__ && global.__marDevMode) setupDevChannel();
    }, bootFailed);
  };

  // setupDevChannel opens the SSE connection used by `mar dev` for both
  // hot reload and dev-time UI feedback (compile errors, server-down
  // detection). Skipped when EventSource isn't available: the app
  // still runs, just without dev affordances.
  //
  // Both the compile-error and connection state are reported to the dev
  // dock as panels; same dock that mountPages uses for the time-travel
  // panel. One bottom-right widget hosts every dev affordance.
  let setupDevChannel = null;
  let getDevDock = null;
  let displayValue = null;
  if (__MAR_DEV__) {
  setupDevChannel = function () {
    if (typeof EventSource === 'undefined') return;

    const dock = getDevDock();

    dock.registerPanel({
      id: 'compile-error',
      badge: {
        icon: '⨯',
        color: '#fca5a5',
        title: 'Compile error',
        label: () => 'compile error',
        visible: (s) => !!s.message,
        pulse: (s) => !!s.message,
      },
      render: (container, state) => {
        const pre = document.createElement('pre');
        pre.style.margin = '0';
        pre.style.whiteSpace = 'pre-wrap';
        pre.style.color = '#fecaca';
        pre.textContent = state.message || '';
        container.appendChild(pre);
      },
      initialState: { message: '' },
    });

    dock.registerPanel({
      id: 'connection',
      badge: {
        icon: '⚠',
        color: '#fcd34d',
        title: 'Connection',
        label: () => 'offline',
        visible: (s) => s.disconnected,
        pulse: (s) => s.disconnected,
      },
      render: (container, state) => {
        container.textContent = state.disconnected
          ? 'Server offline. Reconnecting…'
          : 'Connected.';
      },
      initialState: { disconnected: false },
    });

    let disconnectTimer = null;
    const clearDisconnectTimer = () => {
      if (disconnectTimer) { clearTimeout(disconnectTimer); disconnectTimer = null; }
    };

    const es = new EventSource('/_mar/reload');

    // Tracks whether we've ever had a live connection. The first onopen
    // is the initial connect; any *later* onopen is a reconnect, which
    // on localhost almost always means `mar dev` was restarted.
    let everConnected = false;

    es.onopen = function () {
      clearDisconnectTimer();
      if (everConnected) {
        // The SSE stream dropped and came back: a server restart. Since
        // runtime.js (and the CSS it injects via ensureUIStyles) is
        // embedded in the `mar` binary, a rebuilt binary serves a new
        // runtime.js, but the soft hot-reload path (marReload) only
        // refetches program.json and would keep the STALE bundle + its
        // injected styles running in this tab. That's exactly how a
        // freshly-fixed CSS rule appears not to take effect until a
        // manual refresh. A full reload refetches runtime.js so the new
        // build actually lands. In-process .mar hot-reloads never drop
        // the connection (the same process recompiles and broadcasts a
        // `reload` event), so this only fires on a real restart.
        location.reload();
        return;
      }
      everConnected = true;
      dock.updatePanel('connection', { disconnected: false });
    };

    es.onmessage = function (ev) {
      let payload;
      try { payload = JSON.parse(ev.data); } catch (_) { return; }
      if (payload.type === 'reload') {
        dock.updatePanel('compile-error', { message: '' });
        global.marReload();
      } else if (payload.type === 'ok') {
        dock.updatePanel('compile-error', { message: '' });
      } else if (payload.type === 'error') {
        dock.updatePanel('compile-error', { message: payload.message });
      }
    };

    es.onerror = function () {
      // EventSource fires onerror on every drop. Wait ~1s before
      // surfacing: short blips (server restart inside hot-reload,
      // network hiccup) shouldn't flash a badge.
      clearDisconnectTimer();
      disconnectTimer = setTimeout(function () {
        if (es.readyState !== 1 /* OPEN */) {
          dock.updatePanel('connection', { disconnected: true });
        }
      }, 1000);
    };
  }

  // ---------- Dev dock ----------
  //
  // A single bottom-right widget that hosts pluggable panels: compile
  // errors, connection state, time travel, and (in the future) state
  // inspectors, network logs, etc. Each panel registers a badge that
  // appears in the bar; clicking the badge expands the panel's content
  // above the bar. Only one panel is expanded at a time.
  //
  // The dock is a singleton on window so that hot-reload (which
  // re-creates `currentDispatch` and the page closures) doesn't
  // accidentally instantiate a duplicate.

  getDevDock = function () {
    if (global.__marDevDock) return global.__marDevDock;
    return (global.__marDevDock = createDevDock());
  };

  function createDevDock() {
    // Inject pulse animation once.
    if (!document.getElementById('mar-dock-style')) {
      const style = document.createElement('style');
      style.id = 'mar-dock-style';
      style.textContent =
        '@keyframes mar-dock-pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.55; } }' +
        '.mar-dock-badge { padding: 4px 8px; border-radius: 4px; cursor: pointer; user-select: none; }' +
        '.mar-dock-badge:hover { background: #374151; }' +
        '.mar-dock-badge.active { background: #374151; }' +
        '.mar-dock-badge.pulse { animation: mar-dock-pulse 1.2s ease-in-out infinite; }' +
        // Time-travel scrubber: custom range so the thumb reaches both
        // ends (the native thumb insets by half its width, leaving a gap)
        // and matches the dark dock. The track fills blue up to the
        // cursor via the --tt-fill CSS var (set in renderTimeTravelPanel);
        // Firefox gets the same fill from ::-moz-range-progress.
        '.mar-tt-slider { -webkit-appearance: none; appearance: none; height: 16px; margin: 0; padding: 0; background: transparent; cursor: pointer; vertical-align: middle; }' +
        '.mar-tt-slider::-webkit-slider-runnable-track { height: 4px; border-radius: 2px; background: linear-gradient(to right, #3b82f6 var(--tt-fill, 100%), #4b5563 var(--tt-fill, 100%)); }' +
        '.mar-tt-slider::-webkit-slider-thumb { -webkit-appearance: none; appearance: none; width: 16px; height: 10px; margin-top: -3px; border-radius: 5px; background: #f3f4f6; border: none; box-shadow: 0 1px 2px rgba(0, 0, 0, 0.4); }' +
        '.mar-tt-slider::-moz-range-track { height: 4px; border-radius: 2px; background: #4b5563; }' +
        '.mar-tt-slider::-moz-range-progress { height: 4px; border-radius: 2px; background: #3b82f6; }' +
        '.mar-tt-slider::-moz-range-thumb { width: 16px; height: 10px; border: none; border-radius: 5px; background: #f3f4f6; box-shadow: 0 1px 2px rgba(0, 0, 0, 0.4); }' +
        '.mar-tt-slider:focus { outline: none; }' +
        '.mar-tt-slider:focus-visible::-webkit-slider-thumb { box-shadow: 0 0 0 3px rgba(59, 130, 246, 0.5); }';
      document.head.appendChild(style);
    }

    const root = document.createElement('div');
    root.id = 'mar-dev-dock';
    root.style.position = 'fixed';
    root.style.bottom = '0';
    root.style.right = '8px';
    root.style.zIndex = '9999';
    root.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, monospace';
    root.style.fontSize = '12px';
    root.style.display = 'none';
    // Pin the dock during page view-transitions. The default
    // `::view-transition-old(root)` / `::view-transition-new(root)`
    // snapshots include the whole document, dock and all, so the
    // dock slides with the page despite being at `position: fixed`.
    // Giving it its own `view-transition-name` opts it out of the
    // root group: the browser snapshots the dock as a separate,
    // continuous element across old→new. Combined with the CSS
    // rule disabling animation for that name (see ensureUIStyles),
    // the dock stays visually anchored to the bottom-right while
    // the page slides underneath.
    root.style.viewTransitionName = 'mar-dev-dock';

    const panelEl = document.createElement('div');
    panelEl.style.background = '#1f2937';
    panelEl.style.color = '#f3f4f6';
    panelEl.style.borderRadius = '6px 6px 0 0';
    panelEl.style.boxShadow = '0 -4px 16px rgba(0,0,0,0.3)';
    // Fixed dimensions so the panel doesn't reflow as content grows
    // (e.g. a DraftChanged frame label getting longer with each
    // keystroke, or new frames appearing). Stable size avoids visual
    // jitter while the user is reading or interacting. Capped at 90vw /
    // 90vh for narrow viewports.
    panelEl.style.width = '480px';
    panelEl.style.maxWidth = '90vw';
    panelEl.style.height = '420px';
    panelEl.style.maxHeight = '90vh';
    panelEl.style.display = 'none';
    panelEl.style.flexDirection = 'column';
    panelEl.style.overflow = 'hidden';

    const panelHeader = document.createElement('div');
    panelHeader.style.padding = '6px 10px';
    panelHeader.style.background = '#111827';
    panelHeader.style.display = 'flex';
    panelHeader.style.justifyContent = 'space-between';
    panelHeader.style.alignItems = 'center';
    panelHeader.style.fontWeight = '600';
    const panelTitle = document.createElement('span');
    panelHeader.appendChild(panelTitle);
    const closeBtn = document.createElement('button');
    closeBtn.textContent = '×';
    closeBtn.title = 'Close (Esc)';
    closeBtn.style.background = 'transparent';
    closeBtn.style.border = 'none';
    closeBtn.style.color = '#9ca3af';
    closeBtn.style.cursor = 'pointer';
    closeBtn.style.fontSize = '16px';
    closeBtn.style.lineHeight = '1';
    closeBtn.style.padding = '0 4px';
    closeBtn.onclick = () => collapse();
    panelHeader.appendChild(closeBtn);

    const panelBody = document.createElement('div');
    panelBody.style.padding = '8px 10px';
    panelBody.style.flex = '1';
    panelBody.style.overflow = 'hidden';
    panelBody.style.display = 'flex';
    panelBody.style.flexDirection = 'column';
    panelBody.style.minHeight = '0'; // allow flex children to shrink properly

    panelEl.appendChild(panelHeader);
    panelEl.appendChild(panelBody);

    const bar = document.createElement('div');
    bar.style.background = '#1f2937';
    bar.style.color = '#f3f4f6';
    bar.style.padding = '4px 6px';
    bar.style.borderTopLeftRadius = '6px';
    bar.style.borderTopRightRadius = '6px';
    bar.style.display = 'flex';
    bar.style.gap = '4px';
    bar.style.alignItems = 'center';
    bar.style.boxShadow = '0 -2px 8px rgba(0,0,0,0.2)';

    root.appendChild(panelEl);
    root.appendChild(bar);
    document.body.appendChild(root);

    const panels = []; // [{ id, badge, render, state }]
    const badgeEls = {}; // id -> persistent badge element (stable click target)
    let expandedId = null;

    function rerenderBar() {
      let visibleCount = 0;
      for (const p of panels) {
        const visible = p.badge.visible ? p.badge.visible(p.state) : true;
        if (!visible) {
          if (badgeEls[p.id]) { badgeEls[p.id].remove(); delete badgeEls[p.id]; }
          continue;
        }
        visibleCount++;
        let el = badgeEls[p.id];
        if (!el) {
          // Build the badge ONCE. It (and its handler) then persists across
          // updates, so a high-frequency updatePanel: e.g. a game loop
          // dispatching ~60 msgs/s, can't tear the tap target out from
          // under a press. Only text / colors / classes change below.
          el = document.createElement('span');
          const iconEl = document.createElement('span');
          const labelEl = document.createElement('span');
          labelEl.style.color = '#e5e7eb';
          el.appendChild(iconEl);
          el.appendChild(document.createTextNode(' '));
          el.appendChild(labelEl);
          el._icon = iconEl;
          el._label = labelEl;
          // Toggle on pointerDOWN, not click. A `click` is synthesized only
          // after pointerup AND survives no long task between down/up: under a
          // 60fps dispatch loop the browser routinely drops it, so taps felt
          // dead ("had to click many times"). pointerdown fires on contact.
          el.style.touchAction = 'manipulation';
          el.onpointerdown = (ev) => { ev.preventDefault(); toggle(p.id); };
          badgeEls[p.id] = el;
        }
        el.className = 'mar-dock-badge'
          + (expandedId === p.id ? ' active' : '')
          + (p.badge.pulse && p.badge.pulse(p.state) ? ' pulse' : '');
        el._icon.style.color = p.badge.color || '#f3f4f6';
        el._icon.textContent = p.badge.icon || '•';
        el._label.textContent = p.badge.label ? p.badge.label(p.state) : p.id;
        // Attach only if not already in the bar. Re-appending an existing child
        // MOVES it (remove+insert), and a move landing between a tap's down and
        // up eats the click. Skipping it keeps the tap target perfectly static.
        if (el.parentNode !== bar) bar.appendChild(el);
      }
      root.style.display = visibleCount === 0 ? 'none' : 'block';
      if (expandedId) {
        const cur = panels.find(p => p.id === expandedId);
        if (cur && cur.badge.visible && !cur.badge.visible(cur.state)) collapse();
      }
    }

    function expand(id) {
      const p = panels.find(p => p.id === id);
      if (!p) return;
      expandedId = id;
      timeTravelPanelIsOpen = (id === 'time-travel');   // live pause flag (see render)
      setTimeTravelMuted(id === 'time-travel');         // hush audio while inspecting the past
      // Time-travel gets a wider panel so the model inspector sits in a second
      // column beside the frame timeline; other panels keep the default width.
      panelEl.style.width = (id === 'time-travel') ? '720px' : '480px';
      panelTitle.textContent = p.badge.title || p.id;
      panelBody.innerHTML = '';
      p.render(panelBody, p.state, makeHelpers(p));
      panelEl.style.display = 'flex';
      rerenderBar();
      persistExpanded();
      // Opening time-travel freezes the world at the present frame. render()
      // reads timeTravelPanelIsOpen (set just above) and strips clock subs; poke
      // it now so the pause is immediate rather than one tick later.
      if (id === 'time-travel' && currentDevRerender) currentDevRerender();
    }

    function collapse() {
      const wasId = expandedId;
      expandedId = null;
      timeTravelPanelIsOpen = false;                    // live pause flag: closed => world runs
      setTimeTravelMuted(false);                        // restore audio if WE muted it on open
      panelEl.style.display = 'none';
      rerenderBar();
      persistExpanded();
      // Closing time-travel EXITS travel mode: clear `traveling` and snap the
      // cursor to the latest frame so render() shows the live (present) model
      // again (see drawModel) and the frame-capture tail-follow resumes; the
      // re-render also restarts the clock subs (while paused no timer fires, so
      // this is the only thing that can). Without it, scrubbing back and then
      // closing left `traveling` true: the world stayed frozen on the past frame
      // while the clock kept advancing the hidden live model underneath ("seems
      // to resume but still shows the old state"). Set the flag DIRECTLY on the
      // shared preservedTimeTravel (what render reads) rather than routing
      // through currentJumpToFrame, so it's robust regardless of mount identity;
      // currentDevRerender is the same seam expand()/pause already rely on.
      if (wasId === 'time-travel' && preservedTimeTravel) {
        preservedTimeTravel.cursor = preservedTimeTravel.frames.length - 1; // -1 when empty
        preservedTimeTravel.traveling = false;
        if (currentDevRerender) currentDevRerender();
      }
    }

    function toggle(id) {
      if (expandedId === id) collapse(); else expand(id);
    }

    function rerenderExpanded() {
      if (!expandedId) return;
      const p = panels.find(p => p.id === expandedId);
      if (!p) return;
      // Preserve scroll positions across re-renders so clicking an item
      // in a long list doesn't reset the user's scroll. Panels mark
      // scrollable containers with `data-scroll-key="<id>"`; we snapshot
      // before re-render and restore after.
      const scrolls = {};
      panelBody.querySelectorAll('[data-scroll-key]').forEach(function (el) {
        scrolls[el.getAttribute('data-scroll-key')] = el.scrollTop;
      });
      panelBody.innerHTML = '';
      p.render(panelBody, p.state, makeHelpers(p));
      panelBody.querySelectorAll('[data-scroll-key]').forEach(function (el) {
        const key = el.getAttribute('data-scroll-key');
        if (scrolls[key] !== undefined) el.scrollTop = scrolls[key];
      });
    }

    // Coalesce high-frequency updatePanel() calls (e.g. a game loop's ~60
    // dispatch/s) into ~8 DOM refreshes/s. State is applied immediately in
    // updatePanel; this only paces the re-render of the badge bar and the open
    // panel body, so the panel stays readable/interactive under an event flood.
    let refreshPending = false;
    function scheduleRefresh() {
      if (refreshPending) return;
      refreshPending = true;
      setTimeout(function () {
        refreshPending = false;
        rerenderBar();
        if (expandedId) rerenderExpanded();
      }, 120);
    }

    function makeHelpers(p) {
      return { update: (s) => api.updatePanel(p.id, s) };
    }

    function persistExpanded() {
      try { localStorage.setItem('mar-dev-dock-expanded', expandedId || ''); } catch (_) {}
    }

    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && expandedId) collapse();
    });

    const api = {
      registerPanel(spec) {
        const idx = panels.findIndex(p => p.id === spec.id);
        const panel = {
          id: spec.id,
          badge: spec.badge || {},
          render: spec.render || (() => {}),
          state: spec.initialState || {},
        };
        if (idx >= 0) panels[idx] = panel;
        else panels.push(panel);
        rerenderBar();
        // Lazy-restore previously expanded panel after registration.
        try {
          const want = localStorage.getItem('mar-dev-dock-expanded');
          if (want === spec.id && expandedId !== want) {
            const visible = panel.badge.visible ? panel.badge.visible(panel.state) : true;
            if (visible) expand(spec.id);
          }
        } catch (_) {}
      },
      updatePanel(id, newState) {
        const p = panels.find(p => p.id === id);
        if (!p) return;
        p.state = typeof newState === 'function'
          ? newState(p.state)
          : Object.assign({}, p.state, newState);
        scheduleRefresh();
      },
      getPanelState(id) {
        const p = panels.find(p => p.id === id);
        return p ? p.state : null;
      },
      expand,
      collapse,
    };
    return api;
  }

  // displayValue formats a runtime value as a short, readable string for
  // dev-tool UIs (e.g. listing recent Msgs in the time-travel panel). Not
  // round-trippable; truncates deeply-nested values. Only callers are
  // inside __MAR_DEV__ blocks, so the assignment is dropped from prod.
  displayValue = function (v, depth) {
    if (depth === undefined) depth = 0;
    if (depth > 3) return '…';
    if (v === null || v === undefined) return String(v);
    switch (v.k) {
      case 'I': return String(v.n);
      case 'De': return decStr(v);
      case 'Dv': return '<division>';
      case 'S': return JSON.stringify(v.s);
      case 'B': return v.b ? 'True' : 'False';
      case 'U': return '()';
      case 'TM': return new Date(v.millis).toISOString();
      case 'DT': return formatDateISO(v);
      case 'D': return durationSecondsText(v.ms) + 's';
      case 'L': return '[' + v.xs.map(function (x) { return displayValue(x, depth + 1); }).join(', ') + ']';
      case 'T': return '(' + v.xs.map(function (x) { return displayValue(x, depth + 1); }).join(', ') + ')';
      case 'R': {
        const fields = (v.order || Object.keys(v.fields || {})).map(function (k) {
          return k + ' = ' + displayValue(v.fields[k], depth + 1);
        });
        return '{ ' + fields.join(', ') + ' }';
      }
      case 'C':
        if (!v.args || v.args.length === 0) return v.tag;
        return v.tag + ' ' + v.args.map(function (x) {
          var inner = displayValue(x, depth + 1);
          return /\s/.test(inner) ? '(' + inner + ')' : inner;
        }).join(' ');
      case 'E': return '<effect>';
      case 'SUB': return '<sub>';
      case 'Fn': return '<fn>';
      case 'V': return '<view>';
      default: return '<?>';
    }
  };

  // __marEvalValue, dev/test hook: load a compiled program's modules
  // into a fresh env and render one top-level value by (qualified)
  // name. The cross-runtime conformance test (internal/jsserve/
  // decimal_conformance_test.go) uses it to prove this runtime and the
  // Go runtime compute identical values. esbuild drops the whole
  // __MAR_DEV__ block from production bundles.
  const marEvalRaw = function (program, name) {
    const env = makeBuiltinEnv();
    const modules = Array.isArray(program.modules) ? program.modules : [];
    const modulesByName = Object.create(null);
    for (const m of modules) {
      const nm = (m.name || []).join('.');
      if (nm) modulesByName[nm] = m;
    }
    for (const mod of modules) loadModule(env, mod, modulesByName);
    const v = envLookup(env, name);
    if (v === undefined) throw new Error('value not found: ' + name);
    return v;
  };
  global.__marEvalValue = function (program, name) {
    return displayValue(marEvalRaw(program, name), 0);
  };
  // Same, but WITHOUT rendering: a test that needs to look inside an opaque
  // value (a Sound's voice records, say) cannot do it through a display string.
  global.__marEvalRaw = marEvalRaw;
  // Start one subscription source directly, outside a running page. A Cmd can be
  // driven from a test because it carries .run(); a Sub cannot: it only means
  // something to the reconciler. This lets a test drive the SUB half of the synth
  // (Sound.voice / Sound.glide's held node) against a fake AudioContext, the same way the Cmd
  // half is already driven through Sound.play.
  global.__marStartSub = function (src, sound) { return subSources[src].start({ sound }); };
  // The date input's two halves, for internal/jsserve/datepicker_test.go: the
  // text the control shows for a Date, and the Date a picked text stands for;
  // and the day Date.today reads at an instant, in the device's time zone.
  global.__marTestDateInput = {
    value: (days) => dateInputValue({ attrs: [{ name: 'value', value: VDate(days) }] }),
    parse: (s) => { const dt = parseDateInput(s); return dt ? dt.days : null; },
    today: (ms) => localDay(ms, localOffsets()),
    // A field showing the model's day `days` gets the text `typed`: the days
    // dispatched on change, and the text left on screen after a blur.
    type: (days, typed) => {
      const sent = [];
      const e = {
        value: typed,
        __marView: { attrs: [{ name: 'value', value: VDate(days) }], msg: native(1, ([d]) => d) },
      };
      dateInputChanged(e, (m) => sent.push(m.days));
      const typedStays = e.value;
      dateInputLeft(e);
      return { sent, typedStays, afterBlur: e.value };
    },
  };
  // Run one Cmd outside a page and hand back whatever it dispatches.
  //
  // The client half of a service call had no test at all, which is a large
  // part of why it went unchecked for so long: everything about it happens
  // inside a fetch callback, behind a dispatch that only a mounted page
  // installs. This borrows both — a stubbed fetch and a collector in place of
  // the page's dispatch — so a response body can be pushed through the real
  // path and the resulting Msg read back.
  // `raw` hands back the dispatched Msg itself rather than its display: the
  // wire round trip (internal/jsserve/wire_roundtrip_test.go) has to see a
  // value's KIND, and a display string cannot tell a Duration from an Int.
  global.__marTestRunCmd = function (program, name, raw) {
    const cmd = marEvalRaw(program, name);
    if (!cmd || cmd.k !== 'E' || typeof cmd.run !== 'function') {
      throw new Error(name + ' is not a Cmd');
    }
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => resolve('(nothing dispatched)'), 2000);
      currentDispatch = (msg) => {
        clearTimeout(timer);
        currentDispatch = null;
        try {
          resolve(raw ? msg : displayValue(msg, 0));
        } catch (e) {
          reject(e);
        }
      };
      try {
        cmd.run();
      } catch (e) {
        clearTimeout(timer);
        currentDispatch = null;
        reject(e);
      }
    });
  };
  } // end if (__MAR_DEV__)
})(typeof window !== 'undefined' ? window : globalThis);
