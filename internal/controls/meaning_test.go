package controls

import (
	"strconv"
	"strings"
	"testing"

	"m31labs.dev/cicada/instrument"
	"m31labs.dev/cicada/internal/paramdefs"
	"m31labs.dev/cicada/kernel/voice/keyboard"
)

// Every control says what it does, and every owner says what it is.
func TestEveryControlAndOwnerHasAMeaning(t *testing.T) {
	c := All()
	for _, owner := range c.Owners {
		if owner.Meaning == "" || owner.ValidatedBy == "" || owner.ReadBy == "" {
			t.Errorf("owner %s lacks its meaning or where its truth lives: %+v", owner.Name, owner)
		}
		if owner.Controls == 0 {
			t.Errorf("owner %s has no controls", owner.Name)
		}
	}
	for _, control := range c.Controls {
		if control.Meaning == "" {
			t.Errorf("%s has no meaning; write one beside its definition", control.ID)
		}
		if strings.TrimSpace(control.Meaning) != control.Meaning || strings.HasSuffix(control.Meaning, ".") {
			t.Errorf("%s: a meaning is one line without a closing period: %q", control.ID, control.Meaning)
		}
	}
}

// A meaning table holds no entry for a control that does not exist, so a
// renamed control cannot leave a stale description behind.
func TestMeaningTablesNameOnlyRealControls(t *testing.T) {
	rows := map[string]bool{}
	for _, d := range paramdefs.Registry {
		rows[d.ID] = true
		if paramdefs.IsInstrumentParamSlot(d) {
			continue
		}
		if paramdefs.Meaning(d.ID) == "" {
			t.Errorf("registry row %s has no meaning in internal/paramdefs/meaning.go", d.ID)
		}
	}
	for _, d := range append(append([]paramdefs.Descriptor(nil), paramdefs.HostOnly...), paramdefs.SamplerSettings...) {
		rows[d.ID] = true
		if paramdefs.Meaning(d.ID) == "" {
			t.Errorf("host-only row %s has no meaning in internal/paramdefs/meaning.go", d.ID)
		}
	}
	for _, id := range paramdefs.MeaningIDs() {
		if !rows[id] {
			t.Errorf("internal/paramdefs/meaning.go describes %s, which is no registry, host-only or sampler row", id)
		}
	}
	controls := map[string]map[string]bool{}
	for _, patch := range keyboard.Names {
		family := keyboard.FamilyOf(keyboard.ID(patch)).String()
		if controls[family] == nil {
			controls[family] = map[string]bool{}
		}
		for _, p := range keyboard.Parameters(patch) {
			controls[family][p.Name] = true
			if keyboard.Meaning(patch, p.Name) == "" {
				t.Errorf("keyboard patch %s control %s has no meaning in kernel/voice/keyboard/meaning.go", patch, p.Name)
			}
		}
	}
	for family, names := range keyboard.MeaningNames() {
		for _, name := range names {
			if family == "" {
				found := false
				for _, set := range controls {
					found = found || set[name]
				}
				if !found {
					t.Errorf("kernel/voice/keyboard/meaning.go describes shared control %s, which no patch has", name)
				}
				continue
			}
			if !controls[family][name] {
				t.Errorf("kernel/voice/keyboard/meaning.go describes %s control %s, which no %s patch has", family, name, family)
			}
		}
	}
	declared := map[string]bool{}
	for _, patch := range instrument.Patches() {
		params := PatchParams(patch)
		if len(params) == 0 {
			t.Errorf("library patch %s declares no parameters the catalog can read", patch.ID)
		}
		for _, p := range params {
			declared[p.Name] = true
			if instrument.ParameterMeaning(p.Name) == "" {
				t.Errorf("library patch %s parameter %s has no meaning in instrument/patch_meaning.go", patch.ID, p.Name)
			}
		}
	}
	for _, name := range instrument.ParameterMeaningNames() {
		if !declared[name] {
			t.Errorf("instrument/patch_meaning.go describes %s, which no library patch declares", name)
		}
	}
}

func TestControlIDsAreUniqueAndOwned(t *testing.T) {
	c := All()
	owners := map[string]bool{}
	for _, owner := range c.Owners {
		if owners[owner.Name] {
			t.Errorf("owner %s is listed twice", owner.Name)
		}
		owners[owner.Name] = true
	}
	seen := map[string]bool{}
	for _, control := range c.Controls {
		if seen[control.ID] {
			t.Errorf("control %s is listed twice", control.ID)
		}
		seen[control.ID] = true
		if !owners[control.Owner] {
			t.Errorf("control %s names unknown owner %s", control.ID, control.Owner)
		}
		if control.ID != control.Owner+"."+control.Name {
			t.Errorf("control %s: id is not owner.name", control.ID)
		}
		switch control.Type {
		case "number", "integer":
			if control.Min != nil && control.Max != nil && *control.Min > *control.Max {
				t.Errorf("%s: min %g is above max %g", control.ID, *control.Min, *control.Max)
			}
			if number, ok := control.Default.(float64); ok && control.Min != nil && control.Max != nil && (number < *control.Min || number > *control.Max) && !isolated(control, number) {
				t.Errorf("%s: default %g is outside %g..%g", control.ID, number, *control.Min, *control.Max)
			}
		case "enum", "toggle":
			if len(control.Values) == 0 {
				t.Errorf("%s: an %s lists no values", control.ID, control.Type)
			}
		case "name":
		default:
			t.Errorf("%s: unknown type %q", control.ID, control.Type)
		}
		if len(control.SetIn) == 0 && !control.Refused {
			t.Errorf("%s: says nowhere it can be set", control.ID)
		}
	}
}

func TestCatalogCounts(t *testing.T) {
	counts := All().Count()
	t.Logf("owners %d, controls %d, in the registry %d, by kind %v, directions %+v", counts.Owners, counts.Controls, counts.Registry, counts.ByKind, counts.Directions)
}

func isolated(c Control, v float64) bool {
	for _, word := range c.Values {
		if word == strconv.FormatFloat(v, 'f', -1, 64) {
			return true
		}
	}
	return false
}
