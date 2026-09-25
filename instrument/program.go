// Package instrument compiles custom voice expressions into a typed, bounded
// DSP graph and lowers them to the audio kernel. Compilation never runs code.
package instrument

import (
	"fmt"
	"strconv"
	"strings"

	"m31labs.dev/cicada/notation"
)

type Type string

const (
	Unit  Type = "unit"
	Hz    Type = "hz"
	MS    Type = "ms"
	DB    Type = "db"
	Gate  Type = "gate"
	Audio Type = "audio"
)

type Node struct {
	Op       string
	Type     Type
	Inputs   []int
	Name     string
	Literal  string
	Position notation.Position
}

type Program struct {
	Name          string
	Mode          string
	Nodes         []Node
	Output        int
	StatefulNodes int
}

type compiler struct {
	program Program
	symbols map[string]int
	ds      []notation.Diagnostic
}

// Compile checks names, units, function signatures, and graph size. Each let
// may refer only to parameters, built-in inputs, or earlier lets, so the graph
// is acyclic. Stateful DSP primitives reserve state when an executor loads it.
func Compile(src notation.Instrument) (*Program, []notation.Diagnostic) {
	c := &compiler{program: Program{Name: src.Name, Mode: src.Mode}, symbols: map[string]int{}}
	for _, input := range []struct {
		name   string
		typeOf Type
	}{{"pitch", Hz}, {"gate", Gate}, {"velocity", Unit}, {"sample_rate", Hz}} {
		c.symbols[input.name] = c.emit(Node{Op: "input", Name: input.name, Type: input.typeOf})
	}
	for _, param := range src.Params {
		typ := Type(param.Unit)
		if _, exists := c.symbols[param.Name]; exists {
			c.errorAt("CICADA-DUPLICATE", "duplicate or reserved symbol "+param.Name, param.Position)
			continue
		}
		literalType, err := literalType(param.Default)
		if err != nil || literalType != typ {
			c.errorAt("CICADA-UNIT", fmt.Sprintf("default for %s must have unit %s", param.Name, typ), param.Position)
			continue
		}
		c.symbols[param.Name] = c.emit(Node{Op: "param", Name: param.Name, Type: typ, Literal: param.Default, Position: param.Position})
	}
	for _, let := range src.Lets {
		if _, exists := c.symbols[let.Name]; exists {
			c.errorAt("CICADA-DUPLICATE", "duplicate or reserved symbol "+let.Name, let.Position)
			continue
		}
		index, _ := c.expr(let.Value)
		if index >= 0 {
			c.symbols[let.Name] = index
		}
	}
	output, typ := c.expr(src.Output)
	if output < 0 || typ != Audio {
		c.errorAt("CICADA-UNIT", "out expression must produce audio", src.Position)
	}
	c.program.Output = output
	if len(c.program.Nodes) > 128 {
		c.errorAt("CICADA-LIMIT", "voice graph exceeds 128 nodes", src.Position)
	}
	if c.program.StatefulNodes > 32 {
		c.errorAt("CICADA-LIMIT", "voice graph exceeds 32 stateful nodes", src.Position)
	}
	if len(c.ds) > 0 {
		return nil, c.ds
	}
	return &c.program, nil
}

func (c *compiler) expr(e *notation.Expr) (int, Type) {
	if e == nil {
		return -1, ""
	}
	switch e.Kind {
	case "number":
		typ, err := literalType(e.Text)
		if err != nil {
			c.errorAt("CICADA-PARAM", err.Error(), e.Position)
			return -1, ""
		}
		return c.emit(Node{Op: "literal", Type: typ, Literal: e.Text, Position: e.Position}), typ
	case "name":
		index, ok := c.symbols[e.Text]
		if !ok {
			c.errorAt("CICADA-REFERENCE", "unknown symbol "+e.Text, e.Position)
			return -1, ""
		}
		return index, c.program.Nodes[index].Type
	case "binary":
		left, leftType := c.expr(e.Left)
		right, rightType := c.expr(e.Right)
		if left < 0 || right < 0 {
			return -1, ""
		}
		result, ok := binaryResult(e.Text, leftType, rightType)
		if !ok {
			c.errorAt("CICADA-UNIT", fmt.Sprintf("cannot apply %s to %s and %s", e.Text, leftType, rightType), e.Position)
			return -1, ""
		}
		return c.emit(Node{Op: e.Text, Type: result, Inputs: []int{left, right}, Position: e.Position}), result
	case "call":
		inputs := make([]int, 0, len(e.Args))
		types := make([]Type, 0, len(e.Args))
		for _, arg := range e.Args {
			index, typ := c.expr(arg)
			if index < 0 {
				return -1, ""
			}
			inputs = append(inputs, index)
			types = append(types, typ)
		}
		result, stateful, ok := callResult(e.Text, types)
		if !ok {
			c.errorAt("CICADA-PARAM", fmt.Sprintf("unknown function or wrong argument types: %s", e.Text), e.Position)
			return -1, ""
		}
		if stateful {
			c.program.StatefulNodes++
		}
		return c.emit(Node{Op: e.Text, Type: result, Inputs: inputs, Position: e.Position}), result
	default:
		c.errorAt("CICADA-PARAM", "invalid expression", e.Position)
		return -1, ""
	}
}

func (c *compiler) emit(n Node) int {
	index := len(c.program.Nodes)
	c.program.Nodes = append(c.program.Nodes, n)
	return index
}

func (c *compiler) errorAt(code, message string, position notation.Position) {
	c.ds = append(c.ds, notation.Diagnostic{Code: code, Message: message, Severity: "error", Position: position})
}

func literalType(text string) (Type, error) {
	suffixes := []struct {
		suffix string
		typeOf Type
	}{{"khz", Hz}, {"hz", Hz}, {"ms", MS}, {"db", DB}, {"s", MS}, {"%", Unit}}
	for _, candidate := range suffixes {
		if strings.HasSuffix(strings.ToLower(text), candidate.suffix) {
			number := text[:len(text)-len(candidate.suffix)]
			if _, err := strconv.ParseFloat(number, 64); err != nil {
				return "", err
			}
			return candidate.typeOf, nil
		}
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return "", err
	}
	return Unit, nil
}

func binaryResult(op string, left, right Type) (Type, bool) {
	switch op {
	case "+", "-":
		return left, left == right && left != Gate
	case "*":
		if left == Unit && right != Gate {
			return right, true
		}
		if right == Unit && left != Gate {
			return left, true
		}
	case "/":
		if right == Unit && left != Gate {
			return left, true
		}
		if left == right && left != Audio && left != Gate {
			return Unit, true
		}
	}
	return "", false
}

func callResult(name string, args []Type) (Type, bool, bool) {
	matches := func(want ...Type) bool {
		if len(args) != len(want) {
			return false
		}
		for i, typ := range want {
			if args[i] != typ {
				return false
			}
		}
		return true
	}
	switch name {
	case "saw", "square", "sine":
		return Audio, true, matches(Hz)
	case "noise":
		return Audio, true, matches()
	case "env":
		return Unit, true, matches(Gate, MS)
	case "ladder", "diode":
		return Audio, true, matches(Audio, Hz, Unit)
	case "lowpass", "highpass":
		return Audio, true, matches(Audio, Hz)
	case "mix":
		return Audio, false, matches(Audio, Audio, Unit)
	case "tanh":
		return Audio, false, matches(Audio)
	case "exp2":
		return Unit, false, matches(Unit)
	case "clamp":
		return Unit, false, matches(Unit, Unit, Unit)
	}
	return "", false, false
}
