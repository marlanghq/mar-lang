#!/usr/bin/env python3
"""Myrkheim map generator.

Emits the barrow maps that get pasted into Barrows.mar. They are built
programmatically (carve rooms out of rock, then place glyphs) so the 24x24
shape, the solid border and the glyph placement can be ASSERTED instead of
eyeballed. BFS validates that the player can reach the rune without crossing
a rune door, and the waystone only THROUGH one.

Headings used to live here too, as a table of 240 sines and cosines. They
are gone: the game asks Math.sin and Math.cos directly now (see dirAt).

Run: python3 tools/gen_tables.py > tools/gen_output.txt
"""

import sys
from collections import deque

N = 24
SOLID = set("#%T")          # rune door D / plain door d handled per-check


# ---------------------------------------------------------------- map maker

class Map:
    def __init__(self, name):
        self.name = name
        self.g = [["#"] * N for _ in range(N)]

    def carve(self, x0, y0, x1, y1):
        for y in range(y0, y1 + 1):
            for x in range(x0, x1 + 1):
                assert 1 <= x <= N - 2 and 1 <= y <= N - 2, (self.name, x, y)
                self.g[y][x] = "."

    def put(self, x, y, ch):
        if ch == "T":                       # torch marks a WALL face
            assert self.g[y][x] == "#", ("torch not on wall", self.name, x, y)
        else:
            assert self.g[y][x] == ".", ("glyph not on floor", self.name, x, y, self.g[y][x])
        self.g[y][x] = ch

    def rows(self):
        return ["".join(r) for r in self.g]

    def find(self, ch):
        return [(x, y) for y in range(N) for x in range(N)
                for c in [self.g[y][x]] if c == ch]

    def bfs(self, start, pass_rune_door, pass_crack=False):
        seen = {start}
        q = deque([start])
        while q:
            x, y = q.popleft()
            for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                nx, ny = x + dx, y + dy
                if not (0 <= nx < N and 0 <= ny < N) or (nx, ny) in seen:
                    continue
                c = self.g[ny][nx]
                if c in SOLID:
                    continue
                if c == "D" and not pass_rune_door:
                    continue
                if c == "C" and not pass_crack:
                    continue
                seen.add((nx, ny))
                q.append((nx, ny))
        return seen

    def validate(self, has_rune, expect):
        rows = self.rows()
        assert len(rows) == N and all(len(r) == N for r in rows), self.name
        for i in range(N):                  # sealed border
            for cell in (rows[0][i], rows[N - 1][i], rows[i][0], rows[i][N - 1]):
                assert cell in SOLID, ("border leak", self.name, i)
        (p,) = self.find("P")
        (x,) = self.find("X")
        free = self.bfs(p, pass_rune_door=False)
        full = self.bfs(p, pass_rune_door=True)
        if has_rune:
            (r,) = self.find("R")
            assert r in free, ("rune locked behind rune door", self.name)
        assert x in full, ("waystone unreachable", self.name)
        assert x not in free, ("waystone not gated by rune door", self.name)
        for ch, n in expect.items():
            got = len(self.find(ch))
            assert got == n, ("count", self.name, ch, got, n)
        everything = self.bfs(p, pass_rune_door=True, pass_crack=True)
        for ch in "EWJvkqyRHGpdDoxfaAub":
            for cx, cy in self.find(ch):
                assert (cx, cy) in everything or ch in "dD", ("orphan glyph", self.name, ch, cx, cy)
        # every crack must guard something: at least one cell only
        # reachable once it shatters
        if self.find("C"):
            gated = everything - full
            assert gated, ("crack guards nothing", self.name)
        return self


def barrow1():
    m = Map("HOWE OF ASH")
    m.carve(2, 17, 5, 21)          # spawn cell (small)
    m.carve(6, 18, 11, 19)         # corridor east
    m.carve(12, 15, 15, 21)        # chamber A
    m.carve(16, 17, 16, 18)        # arch between chambers
    m.carve(17, 14, 21, 19)        # chamber B
    m.carve(18, 9, 18, 13)         # corridor north (door)
    m.carve(8, 5, 12, 8)           # west hall
    m.carve(13, 6, 14, 7)          # hall connector
    m.carve(15, 5, 21, 8)          # east hall
    m.carve(4, 7, 7, 7)            # corridor west
    m.carve(2, 3, 5, 7)            # rune room
    m.carve(18, 1, 21, 3)          # waystone vault (NE)
    m.carve(19, 4, 19, 4)          # vault entry (rune door)
    m.carve(3, 14, 5, 15)          # secret cache (behind the spawn wall)
    m.carve(8, 2, 10, 3)           # HIDDEN ARMORY north of the west hall
    m.put(3, 19, "P")
    m.put(18, 12, "d")
    m.put(19, 4, "D")
    m.put(19, 2, "X")
    m.put(3, 4, "R")
    m.g[16][4] = "C"               # cracked wall: spawn room, north face
    m.g[4][9] = "C"                # cracked wall: west hall top -> armory
    m.put(9, 2, "u")               # GUNGNIR hides in the armory (crack it open)
    m.put(8, 2, "G")
    m.put(3, 14, "a")
    m.put(5, 14, "G")
    m.put(13, 16, "o")
    m.put(10, 8, "o")
    for x, y in ((18, 18), (10, 7), (19, 6), (4, 6)):
        m.put(x, y, "E")
    # the spawn cell, the first corridor and chamber A stay EMPTY: the
    # player learns to walk, turn and pick up gold before anything moves.
    # First contact is the slow, telegraphed draugr in chamber B; the
    # vargs hunt the halls beyond the door (one guards the armory crack).
    for x, y in ((11, 8), (16, 6), (21, 5)):
        m.put(x, y, "v")
    for x, y in ((9, 6), (18, 1)):
        m.put(x, y, "H")
    for x, y in ((14, 20), (20, 15), (2, 7), (21, 1), (9, 19), (12, 21)):
        m.put(x, y, "G")
    for x, y in ((13, 4), (17, 9), (7, 17)):
        m.put(x, y, "T")
    for x, y in ((12, 15), (21, 14), (2, 3), (5, 3)):
        if m.g[y][x] == ".":
            m.g[y][x] = "%"
    return m.validate(True, {"E": 4, "W": 0, "v": 3, "H": 2, "G": 8, "D": 1, "d": 1, "C": 2, "o": 2, "x": 0, "a": 1, "u": 1, "b": 0})


def barrow2():
    m = Map("IRON DEEP")
    m.carve(10, 18, 13, 21)        # spawn (S)
    m.carve(11, 17, 12, 17)
    m.carve(9, 13, 14, 16)         # hub (tighter)
    m.carve(5, 14, 8, 14)          # west corridor
    m.carve(1, 9, 4, 14)           # west hall
    m.carve(2, 6, 2, 8)            # shaft to rune room
    m.carve(1, 1, 4, 5)            # rune room (NW deep)
    m.carve(15, 13, 17, 13)        # east corridor
    m.carve(18, 12, 22, 16)        # east hall south
    m.carve(20, 11, 20, 11)        # hall stair
    m.carve(18, 8, 22, 10)         # east hall north
    m.carve(11, 7, 12, 12)         # north corridor
    m.carve(9, 3, 14, 6)           # north junction
    m.carve(15, 5, 16, 5)          # rune door 1 passage
    m.carve(17, 3, 22, 5)          # outer vault
    m.carve(19, 2, 19, 2)          # rune door 2
    m.carve(18, 1, 22, 1)          # inner vault
    m.carve(2, 16, 4, 17)          # secret cache SW
    m.carve(15, 18, 17, 19)        # secret cache SE
    m.carve(6, 9, 8, 10)           # HIDDEN BOWYER'S NOOK east of the west hall
    m.put(11, 20, "P")
    m.put(8, 14, "d")
    m.put(15, 13, "d")
    m.put(16, 5, "D")
    m.put(19, 2, "D")
    m.put(21, 1, "X")
    m.put(2, 2, "R")
    m.g[15][3] = "C"               # crack under the west hall
    m.g[18][14] = "C"              # crack beside the spawn room
    m.g[10][5] = "C"               # crack: west hall east face -> bowyer's nook
    m.put(7, 9, "b")               # the BOW waits walled up with its arrows
    m.put(8, 10, "o")
    m.put(3, 16, "f")
    m.put(2, 17, "G")
    m.put(16, 18, "A")
    m.put(15, 19, "x")
    m.put(17, 19, "G")
    m.put(13, 14, "o")
    m.put(10, 4, "x")
    m.put(19, 9, "o")
    m.put(2, 11, "a")
    for x, y in ((10, 14), (2, 7), (19, 14), (21, 9), (11, 5), (13, 4)):
        m.put(x, y, "E")
    for x, y in ((13, 15), (21, 4)):
        m.put(x, y, "v")
    for x, y in ((19, 15), (12, 10)):
        m.put(x, y, "k")
    for x, y in ((3, 10), (3, 3), (20, 13)):
        m.put(x, y, "W")
    for x, y in ((21, 15), (14, 3)):
        m.put(x, y, "H")
    for x, y in ((1, 13), (2, 9), (4, 1), (19, 8), (22, 12), (9, 3), (14, 6), (17, 3)):
        m.put(x, y, "G")
    for x, y in ((8, 13), (13, 17), (10, 2), (17, 12)):
        m.put(x, y, "T")
    for x, y in ((9, 16), (18, 16), (1, 9), (22, 16)):
        if m.g[y][x] == ".":
            m.g[y][x] = "%"
    return m.validate(True, {"E": 6, "W": 3, "v": 2, "k": 2, "H": 2, "G": 10, "D": 2, "d": 2, "C": 3, "o": 3, "x": 2, "f": 1, "a": 1, "A": 1, "b": 1})


def barrow3():
    m = Map("HELS THRESHOLD")
    m.carve(1, 18, 4, 22)          # spawn (W)
    m.carve(5, 19, 17, 20)         # gauntlet corridor
    m.carve(8, 16, 9, 18)          # ambush pocket N
    m.carve(12, 21, 13, 22)        # ambush pocket S
    m.carve(15, 16, 16, 18)        # wraith perch
    m.carve(18, 19, 18, 19)        # supply door gap
    m.carve(19, 18, 22, 21)        # supply room
    m.carve(20, 12, 20, 17)        # north corridor
    m.carve(15, 8, 20, 11)         # pre-arena hall
    m.carve(12, 9, 14, 9)          # arena approach (door)
    m.carve(3, 4, 11, 12)          # ARENA 9x9
    m.carve(7, 2, 7, 3)            # arena exit shaft (rune door)
    m.carve(6, 1, 8, 1)            # waystone nook
    m.carve(8, 22, 10, 22)         # secret cache under the gauntlet
    m.carve(16, 5, 18, 6)          # secret cache above the pre-arena hall
    for x, y in ((5, 6), (9, 6), (5, 10), (9, 10)):   # pillars
        m.g[y][x] = "#"
    m.put(2, 20, "P")
    m.put(13, 9, "d")
    m.put(18, 19, "d")
    m.put(7, 3, "D")
    m.put(7, 1, "X")
    m.put(7, 6, "J")
    m.g[21][9] = "C"               # crack in the gauntlet floor wall
    m.g[7][17] = "C"               # crack above the pre-arena hall
    m.put(8, 22, "f")
    m.put(10, 22, "o")
    m.put(17, 5, "A")
    m.put(16, 6, "x")
    m.put(18, 6, "G")
    m.put(20, 18, "o")
    m.put(20, 21, "x")
    m.put(16, 10, "f")
    m.put(4, 5, "o")
    for x, y in ((4, 6), (10, 5), (4, 11), (10, 11)):
        m.put(x, y, "p")
    for x, y in ((8, 16), (13, 22), (20, 14), (16, 9), (11, 19)):
        m.put(x, y, "E")
    for x, y in ((10, 20),):
        m.put(x, y, "v")
    for x, y in ((14, 19),):
        m.put(x, y, "k")
    for x, y in ((16, 17), (18, 10)):
        m.put(x, y, "q")
    for x, y in ((15, 16), (15, 8)):
        m.put(x, y, "W")
    for x, y in ((21, 20), (15, 11)):
        m.put(x, y, "H")
    for x, y in ((19, 20), (22, 18), (20, 8), (10, 19), (3, 12)):
        m.put(x, y, "G")
    for x, y in ((7, 18), (14, 21), (14, 8), (2, 3)):
        m.put(x, y, "T")
    for x, y in ((3, 4), (11, 4), (11, 12)):
        if m.g[y][x] == ".":
            m.g[y][x] = "%"
    m.validate(False, {"E": 5, "W": 2, "v": 1, "k": 1, "q": 2, "H": 2, "G": 6, "D": 1, "d": 2, "J": 1, "p": 4, "C": 2, "o": 3, "x": 2, "f": 2, "A": 1})
    # the Jarl must be reachable without the rune (he DROPS it)
    (p,) = m.find("P")
    (j,) = m.find("J")
    assert j in m.bfs(p, pass_rune_door=False), "jarl locked behind rune door"
    return m


def barrow4():
    # THE DROWNED VAULT — a flooded crypt. Introduces the draug-archer (y):
    # ranged bowmen holding the long sightlines while melee closes.
    m = Map("THE DROWNED VAULT")
    m.carve(2, 20, 4, 22)          # spawn (SW)
    m.carve(5, 21, 10, 21)         # hall east
    m.carve(9, 16, 14, 21)         # room A (SE)
    m.carve(11, 11, 12, 16)        # north corridor
    m.carve(7, 5, 16, 11)          # central chamber (the sunken vault)
    m.carve(2, 6, 6, 10)           # rune room (W)
    m.carve(6, 8, 7, 8)            # rune-room -> chamber connector
    m.carve(16, 7, 20, 9)          # east hall
    m.carve(19, 5, 19, 6)          # vault shaft (rune door)
    m.carve(18, 2, 21, 5)          # waystone vault (NE)
    m.carve(2, 14, 5, 17)          # SW cache
    m.carve(5, 16, 8, 16)          # cache -> room A connector
    m.put(3, 21, "P")
    m.put(19, 6, "D")
    m.put(20, 3, "X")
    m.put(3, 8, "R")
    m.put(2, 17, "H")
    m.put(16, 5, "H")
    m.put(4, 14, "f")
    m.put(10, 7, "o")
    m.put(11, 5, "x")
    for x, y in ((2, 6), (5, 21), (20, 9), (12, 16), (3, 15)):
        m.put(x, y, "G")
    for x, y in ((12, 20), (9, 6), (15, 8), (4, 16)):
        m.put(x, y, "E")
    for x, y in ((13, 9), (18, 8)):
        m.put(x, y, "y")
    m.put(8, 10, "W")
    m.put(11, 19, "v")
    m.put(14, 6, "q")
    for x, y in ((13, 4), (10, 4), (8, 20)):
        m.put(x, y, "T")
    return m.validate(True, {"E": 4, "y": 2, "W": 1, "v": 1, "q": 1, "H": 2,
                             "G": 5, "D": 1, "d": 0, "o": 1, "x": 1, "f": 1, "T": 3})


def barrow5():
    # JAWS OF NIDHOGG — the root of the world, the last barrow. The full
    # bestiary at its meanest: packs of archers and casters over the melee.
    m = Map("JAWS OF NIDHOGG")
    m.carve(2, 2, 4, 4)            # spawn (NW)
    m.carve(5, 3, 11, 3)           # hall east
    m.carve(9, 4, 14, 10)          # room A
    m.carve(11, 10, 12, 15)        # south corridor
    m.carve(6, 14, 17, 21)         # the arena (central, big)
    m.carve(2, 16, 5, 21)          # rune room (SW)
    m.carve(5, 19, 6, 19)          # rune room -> arena connector
    m.carve(15, 4, 20, 9)          # east wing
    m.carve(19, 3, 19, 3)          # vault shaft (rune door)
    m.carve(18, 1, 20, 2)          # waystone vault (NE)
    m.put(3, 3, "P")
    m.put(19, 3, "D")
    m.put(19, 1, "X")
    m.put(3, 20, "R")
    m.put(3, 2, "H")
    m.put(20, 9, "H")
    m.put(2, 21, "f")
    m.put(17, 4, "A")
    m.put(10, 4, "o")
    m.put(15, 21, "o")
    m.put(11, 14, "x")
    for x, y in ((5, 3), (2, 16), (17, 21), (12, 10), (6, 14), (20, 4)):
        m.put(x, y, "G")
    for x, y in ((10, 7), (12, 5), (8, 16), (14, 18), (11, 20), (16, 6)):
        m.put(x, y, "E")
    for x, y in ((13, 9), (16, 15), (9, 18)):
        m.put(x, y, "y")
    for x, y in ((7, 20), (17, 8)):
        m.put(x, y, "W")
    for x, y in ((13, 20), (10, 15)):
        m.put(x, y, "v")
    m.put(14, 16, "k")
    m.put(16, 20, "q")
    for x, y in ((5, 2), (8, 13), (18, 10)):
        m.put(x, y, "T")
    return m.validate(True, {"E": 6, "y": 3, "W": 2, "v": 2, "k": 1, "q": 1,
                             "H": 2, "G": 6, "D": 1, "d": 0, "o": 2, "x": 1,
                             "f": 1, "A": 1, "T": 3})


def emit_map(name, ident, m):
    print()
    print("%s : List String" % ident)
    print("%s =" % ident)
    rows = m.rows()
    print('    [ "%s"' % rows[0])
    for r in rows[1:]:
        print('    , "%s"' % r)
    print("    ]")


def main():
    emit_map("HOWE OF ASH", "map1", barrow1())
    emit_map("IRON DEEP", "map2", barrow2())
    emit_map("HELS THRESHOLD", "map3", barrow3())
    emit_map("THE DROWNED VAULT", "map4", barrow4())
    emit_map("JAWS OF NIDHOGG", "map5", barrow5())
    print("\n-- all maps validated: 24x24, sealed border, rune reachable", file=sys.stderr)
    print("-- pre-rune-door, waystone gated, glyph counts exact", file=sys.stderr)


if __name__ == "__main__":
    main()
