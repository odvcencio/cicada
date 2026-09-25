package instrument

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"m31labs.dev/cicada/kernel/graph"
)

// Lower translates the checked instrument graph to the fixed-size kernel
// program. overrides are per-track parameter values in Cicada notation.
func Lower(program *Program, overrides map[string]string) (graph.Program, error) {
	var out graph.Program
	if program == nil || len(program.Nodes) == 0 || len(program.Nodes) > graph.MaxNodes || program.Output < 0 {
		return out, fmt.Errorf("invalid instrument program")
	}
	declared := make(map[string]bool)
	for _, node := range program.Nodes {
		if node.Op == "param" {
			declared[node.Name] = true
		}
	}
	keys := make([]string, 0, len(overrides))
	for name := range overrides {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if !declared[name] {
			return out, fmt.Errorf("unknown instrument parameter %s", name)
		}
	}
	out.Len = uint8(len(program.Nodes))
	out.Output = uint8(program.Output)
	for i, node := range program.Nodes {
		op, err := kernelOp(node)
		if err != nil {
			return out, err
		}
		kn := graph.Node{Op: op}
		if len(node.Inputs) > 0 {
			kn.A = uint8(node.Inputs[0])
		}
		if len(node.Inputs) > 1 {
			kn.B = uint8(node.Inputs[1])
		}
		if len(node.Inputs) > 2 {
			kn.C = uint8(node.Inputs[2])
		}
		if node.Op == "literal" || node.Op == "param" {
			literal := node.Literal
			if node.Op == "param" {
				if replacement, ok := overrides[node.Name]; ok {
					literal = replacement
				}
			}
			typ, err := literalType(literal)
			if err != nil || typ != node.Type {
				return out, fmt.Errorf("value for %s has wrong unit", node.Name)
			}
			kn.Value, err = numericLiteral(literal)
			if err != nil {
				return out, err
			}
		}
		out.Nodes[i] = kn
	}
	return out, nil
}

func kernelOp(n Node) (graph.Op, error) {
	switch n.Op {
	case "input":
		switch n.Name {
		case "pitch":
			return graph.Pitch, nil
		case "gate":
			return graph.Gate, nil
		case "velocity":
			return graph.Velocity, nil
		case "sample_rate":
			return graph.SampleRate, nil
		}
	case "literal", "param":
		return graph.Constant, nil
	case "+":
		return graph.Add, nil
	case "-":
		return graph.Subtract, nil
	case "*":
		return graph.Multiply, nil
	case "/":
		return graph.Divide, nil
	case "saw":
		return graph.Saw, nil
	case "square":
		return graph.Square, nil
	case "sine":
		return graph.Sine, nil
	case "noise":
		return graph.Noise, nil
	case "env":
		return graph.Envelope, nil
	case "ladder":
		return graph.Ladder, nil
	case "diode":
		return graph.Diode, nil
	case "lowpass":
		return graph.Lowpass, nil
	case "highpass":
		return graph.Highpass, nil
	case "mix":
		return graph.Mix, nil
	case "tanh":
		return graph.Tanh, nil
	case "exp2":
		return graph.Exp2, nil
	case "clamp":
		return graph.Clamp, nil
	}
	return 0, fmt.Errorf("unsupported graph operation %q", n.Op)
}

func numericLiteral(text string) (float32, error) {
	scale := 1.0
	for _, suffix := range []struct {
		suffix string
		scale  float64
	}{{"khz", 1000}, {"hz", 1}, {"ms", 1}, {"db", 1}, {"s", 1000}, {"%", 0.01}} {
		if strings.HasSuffix(strings.ToLower(text), suffix.suffix) {
			text = text[:len(text)-len(suffix.suffix)]
			scale = suffix.scale
			break
		}
	}
	n, err := strconv.ParseFloat(text, 32)
	if err != nil {
		return 0, err
	}
	return float32(n * scale), nil
}
