package edit

import (
	"fmt"

	"m31labs.dev/cicada/notation"
)

// ReplaceText saves a complete buffer verbatim, after Apply validates it.
type ReplaceText struct {
	Source string `json:"source"`
}

func (ReplaceText) Kind() string { return "replacetext" }

// ReplaceDeclaration replaces one declaration's syntax span. Declaration is
// a CST node type, or "pattern" for any pattern type. Name is empty for
// unnamed declarations such as title_decl and seed_decl.
type ReplaceDeclaration struct {
	Declaration string `json:"declaration"`
	Name        string `json:"name,omitempty"`
	Text        string `json:"text"`
}

func (ReplaceDeclaration) Kind() string { return "replacedeclaration" }

func init() {
	Register("replacetext", func() Intent { return &ReplaceText{} })
	Register("replacedeclaration", func() Intent { return &ReplaceDeclaration{} })
	Handle("replacetext", func(ctx *Context, intent Intent) error {
		ctx.Source, ctx.plan = []byte(intent.(*ReplaceText).Source), nil
		ctx.SetLabel("Source saved")
		return nil
	})
	Handle("replacedeclaration", replaceDeclaration)
}

type textDeclaration struct {
	start, end       int
	kind, name, text string
}

func textDeclarations(source []byte) ([]textDeclaration, error) {
	document, err := notation.ParseDocument(source)
	if err != nil {
		return nil, err
	}
	var declarations []textDeclaration
	for i := 0; i < document.Root.NamedChildCount(); i++ {
		node := document.Root.NamedChild(i)
		kind := document.Walker.Type(node)
		if kind == "comment" {
			continue
		}
		declarations = append(declarations, textDeclaration{int(node.StartByte()), int(node.EndByte()), kind, document.Walker.Text(document.Walker.Field(node, "name")), document.Walker.Text(node)})
	}
	return declarations, nil
}

func declarationMatches(kind, requested string) bool {
	if kind == requested {
		return true
	}
	if requested == "pattern" {
		for _, candidate := range patternTypes {
			if kind == candidate {
				return true
			}
		}
	}
	return false
}

func replaceDeclaration(ctx *Context, intent Intent) error {
	in := intent.(*ReplaceDeclaration)
	before, err := textDeclarations(ctx.Source)
	if err != nil {
		return err
	}
	var target *textDeclaration
	for i := range before {
		if declarationMatches(before[i].kind, in.Declaration) && before[i].name == in.Name {
			target = &before[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("unknown declaration %q", in.Name)
	}
	after, err := textDeclarations([]byte(in.Text))
	if err != nil {
		return err
	}
	if len(after) != 1 || !declarationMatches(after[0].kind, in.Declaration) || after[0].name != in.Name {
		return fmt.Errorf("replacement must contain one matching declaration")
	}
	updated, err := ReplaceSpan(ctx.Source, ctx.Options.Path, Span{target.start, target.end, in.Text, ctx.Options.Path})
	if err != nil {
		return err
	}
	ctx.Source, ctx.plan = updated, nil
	ctx.SetLabel("Source saved")
	return nil
}

// ReplacePreviewBar replaces the first generated note pattern and seed,
// preserving the header, other patterns and arrangement. Hosts compile the
// candidate before accepting it; this helper validates notation only.
func ReplacePreviewBar(original, variation string) (string, error) {
	read := func(source string) (map[string]textDeclaration, error) {
		declarations, err := textDeclarations([]byte(source))
		if err != nil {
			return nil, err
		}
		found := map[string]textDeclaration{}
		for _, d := range declarations {
			kind := d.kind
			if kind == "acid_pattern" || kind == "note_pattern" {
				kind = "pattern"
			}
			if kind != "pattern" && kind != "seed_decl" {
				continue
			}
			if _, exists := found[kind]; !exists {
				found[kind] = d
			}
		}
		if _, ok := found["pattern"]; !ok {
			return nil, fmt.Errorf("phrase preview has no acid bar")
		}
		return found, nil
	}
	before, err := read(original)
	if err != nil {
		return "", err
	}
	after, err := read(variation)
	if err != nil {
		return "", err
	}
	if before["pattern"].name != after["pattern"].name {
		return "", fmt.Errorf("phrase preview pattern changed")
	}
	pattern := before["pattern"]
	updated := original[:pattern.start] + after["pattern"].text + original[pattern.end:]
	if seed, ok := before["seed_decl"]; ok {
		newSeed, exists := after["seed_decl"]
		if !exists || seed.end > pattern.start {
			return "", fmt.Errorf("phrase preview seed is unavailable")
		}
		updated = updated[:seed.start] + newSeed.text + updated[seed.end:]
	}
	_, diagnostics := notation.Parse([]byte(updated))
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return "", fmt.Errorf("variation is invalid: %s", d.Message)
		}
	}
	return updated, nil
}
