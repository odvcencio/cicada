#!/usr/bin/env python3
"""List SIMD opcodes by function using Binaryen's decoded instruction output."""
import collections
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys

if len(sys.argv) != 2:
    raise SystemExit("usage: python3 tools/audio/wasm-simd-audit.py kernel.wasm")

binary = Path(sys.argv[1]).read_bytes()
wat = subprocess.run(
    ["wasm-opt", sys.argv[1], "--print", "-o", os.devnull],
    check=True, capture_output=True, text=True,
).stdout
functions = list(re.finditer(r"^ \(func (\S+)", wat, re.MULTILINE))
opcodes = re.compile(r"\(((?:v128|f32x4|f64x2|i8x16|i16x8|i32x4|i64x2)\.[\w_]+)")
arithmetic = {"add", "sub", "mul", "div", "abs", "neg", "sqrt", "min", "max", "pmin", "pmax", "ceil", "floor", "nearest", "trunc"}
total = collections.Counter()
entries = []
for i, match in enumerate(functions):
    end = functions[i + 1].start() if i + 1 < len(functions) else len(wat)
    counts = collections.Counter(opcodes.findall(wat[match.start():end]))
    total.update(counts)
    floating = {op: n for op, n in counts.items() if op.startswith(("f32x4.", "f64x2.")) and op.split(".")[1] in arithmetic}
    if counts:
        entries.append({"function": match.group(1), "opcodes": dict(sorted(counts.items())), "floatingPointArithmetic": floating})

print(json.dumps({
    "rawBytes": len(binary), "wasmSHA256": hashlib.sha256(binary).hexdigest(),
    "simdInstructions": sum(total.values()), "opcodes": dict(sorted(total.items())),
    "functions": entries,
}, indent=2))
