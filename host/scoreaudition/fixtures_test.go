package scoreaudition

import "strings"

// Fixtures are small scores written to exercise one property each.

const skankMajor = `cicada 2
title "Skank"
tempo 120
key c major
seed 1

instrument pluck {
  voice poly {
    out = saw(pitch) * env(gate, 140ms) * velocity * 0.3
  }
}
instrument organ {
  voice poly {
    out = square(pitch) * env(gate, 120ms) * velocity * 0.25
  }
}
instrument bassvox {
  voice mono {
    out = sine(pitch) * env(gate, 350ms) * velocity * 0.6
  }
}

track guitar pluck {}
track piano organ {}
track bass bassvox {}
track kit drums {
  level = -14dB
}

pattern guitar-skank notes {
  . . [c4 e4 g4] . . . [c4 e4 g4] . . . [c4 e4 g4] . . . [c4 e4 g4] .
  . . [c4 f4 a4] . . . [c4 f4 a4] . . . [c4 f4 a4] . . . [c4 f4 a4] .
  . . [d4 g4 b4] . . . [d4 g4 b4] . . . [d4 g4 b4] . . . [d4 g4 b4] .
  . . [c4 e4 g4] . . . [c4 e4 g4] . . . [c4 e4 g4] . . . [c4 e4 g4] .
}
pattern bass-line notes {
  c2 . . . c2 . . . c2 . . . c2 . . .
  f2 . . . f2 . . . f2 . . . f2 . . .
  g2 . . . g2 . . . g2 . . . g2 . . .
  c2 . . . c2 . . . c2 . . . c2 . . .
}
pattern beat drums {
  bd: x... x... x... x...
  sd: .... .... .... ....
  ch: .... .... .... ....
}
scene groove {
  guitar = guitar-skank
  piano = guitar-skank
  bass = bass-line
  kit = beat
}
song { groove*4 }
`

const hornsMinor = `cicada 2
title "Horns"
tempo 100
key a minor
seed 1

instrument horn {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 200ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}
instrument click {
  voice poly {
    out = square(pitch) * env(gate, 60ms) * velocity * 0.15
  }
}

track horns horn {}
track skank click {}

pattern long-chords notes {
  [a3 c4 e4] - - - - - - - - - - - - - - -
  [f3 a3 c4] - - - - - - - - - - - - - - -
  [g3 b3 d4] - - - - - - - - - - - - - - -
  [a3 c4 e4] - - - - - - - - - - - - - - -
}
pattern off notes {
  . . [a4 c5 e5] . . . [a4 c5 e5] . . . [a4 c5 e5] . . . [a4 c5 e5] .
  . . [f4 a4 c5] . . . [f4 a4 c5] . . . [f4 a4 c5] . . . [f4 a4 c5] .
  . . [g4 b4 d5] . . . [g4 b4 d5] . . . [g4 b4 d5] . . . [g4 b4 d5] .
  . . [a4 c5 e5] . . . [a4 c5 e5] . . . [a4 c5 e5] . . . [a4 c5 e5] .
}
scene verse {
  horns = long-chords
  skank = off
}
song { verse*4 }
`

const noteLengths = `cicada 2
title "Lengths"
tempo 132
key a minor
seed 1

instrument voice {
  voice mono {
    let amp = adsr(gate, 4ms, 20ms, 0.9, 20ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}

track stab voice {}
track drone voice {}

pattern stabs notes {
  gate = 50%
  a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3
}
pattern held notes {
  gate = 100%
  a2 - - - - - - - - - - - - - - -
  a2 - - - - - - - - - - - - - - -
  a2 - - - - - - - - - - - - - - -
  a2 - - - - - - - - - - - - - - -
}
pattern stabs4 notes {
  gate = 50%
  a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3
  a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3 a3
}
scene both {
  stab = stabs4
  drone = held
}
song { both*4 }
`

const maskedPart = `cicada 2
title "Masked"
tempo 120
key f major
seed 1

instrument pad {
  voice poly {
    let amp = adsr(gate, 30ms, 100ms, 0.9, 200ms)
    out = saw(pitch) * amp * velocity * 0.3
  }
}

track pad pad {
  level = -40dB
}
track kit drums {}

pattern chords notes {
  [f3 a3 c4] - - - - - - - - - - - - - - -
  [f3 a3 c4] - - - - - - - - - - - - - - -
  [bb3 d4 f4] - - - - - - - - - - - - - - -
  [c4 e4 g4] - - - - - - - - - - - - - - -
}
pattern loud drums {
  bd: X.x. X.x. X.x. X.x.
  sd: .... X... .... X...
  ch: xxxx xxxx xxxx xxxx
  oh: ..x. ..x. ..x. ..x.
}
scene all {
  pad = chords
  kit = loud
}
song { all*4 }
`

const shuffle = `cicada 2
title "Shuffle"
tempo 110
key a minor
seed 1

instrument tick {
  voice mono {
    out = square(pitch) * env(gate, 50ms) * velocity * 0.4
  }
}

track ticks tick {}

pattern swung notes {
  swing = 66%
  . a3 . a3 . a3 . a3 . a3 . a3 . a3 . a3
}
scene s { ticks = swung }
song { s*4 }
`

// maskedPart's pad is inaudible. drumHeavy keeps the same loud drums but lets
// the pad play its chords about 20 dB under the kit, which is quiet but pitched.
var drumHeavy = strings.Replace(maskedPart, "level = -40dB", "level = -32dB", 1)

const swungTwoPatterns = `cicada 2
title "Two swings"
tempo 110
key a minor
seed 1

instrument tick {
  voice mono {
    out = square(pitch) * env(gate, 50ms) * velocity * 0.4
  }
}

track high tick {}
track low tick {}

pattern a notes {
  swing = 60%
  . a4 . a4 . a4 . a4 . a4 . a4 . a4 . a4
}
pattern b notes {
  swing = 60%
  . a2 . a2 . a2 . a2 . a2 . a2 . a2 . a2
}
scene s {
  high = a
  low = b
}
song { s*2 }
`
