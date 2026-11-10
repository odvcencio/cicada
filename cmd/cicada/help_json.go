package main

import (
	"encoding/json"
	"io"
	"sort"
	"strings"
)

// helpSchema names the format of cicada help --json.
const helpSchema = "cicada.help.v1"

type helpDocument struct {
	Schema   string            `json:"schema"`
	Usage    string            `json:"usage"`
	Commands []helpCommandJSON `json:"commands"`
}

type helpCommandJSON struct {
	Name    string         `json:"name"`
	Summary string         `json:"summary"`
	Usage   []string       `json:"usage"`
	Flags   []helpFlagJSON `json:"flags"`
	Notes   string         `json:"notes,omitempty"`
}

// helpFlagJSON is one flag. Argument is the value it takes, such as <file>
// or <bool>; it is empty for a switch.
type helpFlagJSON struct {
	Name        string `json:"name"`
	Argument    string `json:"argument,omitempty"`
	Description string `json:"description"`
}

// writeHelpJSON writes every command, or the one named, with its summary,
// usage lines and flags, sorted by name.
func writeHelpJSON(w io.Writer, only string) bool {
	var commands []helpCommandJSON
	for _, entries := range [][]commandHelpEntry{commandHelpEntries, extraCommandHelpEntries} {
		for _, entry := range entries {
			if only == "" || entry.name == only {
				commands = append(commands, helpCommandFromEntry(entry))
			}
		}
	}
	if len(commands) == 0 {
		return false
	}
	sort.Slice(commands, func(i, j int) bool { return commands[i].Name < commands[j].Name })
	data, err := json.MarshalIndent(helpDocument{Schema: helpSchema, Usage: "cicada <command> [arguments]", Commands: commands}, "", "  ")
	if err != nil {
		return false
	}
	w.Write(append(data, '\n'))
	return true
}

func helpCommandFromEntry(entry commandHelpEntry) helpCommandJSON {
	command := helpCommandJSON{Name: entry.name, Summary: entry.summary, Notes: entry.notes, Flags: []helpFlagJSON{}}
	for _, line := range strings.Split(entry.usage, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			command.Usage = append(command.Usage, line)
		}
	}
	flags := entry.flags
	if !strings.Contains(flags, "--help") {
		flags += "\n  --help      show this help"
	}
	for _, line := range strings.Split(flags, "\n") {
		if flag, ok := parseHelpFlag(line); ok {
			command.Flags = append(command.Flags, flag)
		}
	}
	return command
}

// parseHelpFlag reads one flag line of a help entry: the flag and its
// argument, then two or more spaces, then the description. A few lines use
// one space; there the argument is the run of <...> words after the flag.
func parseHelpFlag(line string) (helpFlagJSON, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "-") {
		return helpFlagJSON{}, false
	}
	spec, description, found := strings.Cut(line, "  ")
	if !found {
		fields := strings.Fields(line)
		end := 1
		for end < len(fields) && strings.HasPrefix(fields[end], "<") {
			end++
		}
		spec, description = strings.Join(fields[:end], " "), strings.Join(fields[end:], " ")
	}
	name, argument, _ := strings.Cut(strings.TrimSpace(spec), " ")
	if flagName, value, ok := strings.Cut(name, "="); ok {
		name, argument = flagName, value
	}
	return helpFlagJSON{Name: name, Argument: strings.TrimSpace(argument), Description: strings.TrimSpace(description)}, true
}
