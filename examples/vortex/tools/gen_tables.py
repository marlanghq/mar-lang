#!/usr/bin/env python3
"""Regenerate the sine table pasted into Main.mar.

Mar has no floating point, so every angle in the game is a BRAD: 1/256 of a
turn, which makes wrapping a `modBy 256` instead of a division by 360. The
table holds one quarter turn — 65 entries, sin(0) through sin(90°) — scaled by
1024; the other three quarters are reflections, and cosine is the same table
read a quarter turn along. See `sinB` in Main.mar.

    python3 tools/gen_tables.py        # prints the literal to paste
"""

import math

QUARTER = 64          # brads in a quarter turn
FP = 1024             # fixed-point scale, matching `one` in Main.mar
WIDTH = 72            # keep the pasted literal inside the file's margin


def quarter_sine():
    return [round(math.sin(i * math.pi / (2 * QUARTER)) * FP)
            for i in range(QUARTER + 1)]


def as_mar_list(values):
    lines, row = [], "    ["
    for i, v in enumerate(values):
        piece = str(v) if i == 0 else ", " + str(v)
        if len(row) + len(piece) > WIDTH:
            lines.append(row)
            row = "    , " + str(v)
        else:
            row += piece
    lines.append(row)
    lines.append("    ]")
    return "\n".join(lines)


if __name__ == "__main__":
    print("sinQuarter : List Int")
    print("sinQuarter =")
    print(as_mar_list(quarter_sine()))
