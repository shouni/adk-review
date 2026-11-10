// Package assets provides prompt templates and other files as embedded resources.
package assets

import (
	"embed"
	"fmt"
	"log/slog"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/shouni/go-prompt-kit/frontmatter"
	"github.com/shouni/go-prompt-kit/resource"
	"go.yaml.in/yaml/v3"
)

const (
	promptDir  = "prompts"
	partialDir = "partials"

	// partialPrefix is the prefix used when registering shared fragments as template names.
	// It is the same value as go-prompt-kit's default; anything carrying it is not exposed as a mode.
	partialPrefix = "_"
)

var (
	// promptFiles are the review prompts. The file name is the mode name.
	//go:embed prompts/*.md
	promptFiles embed.FS

	// partialFiles are text fragments shared by several prompt modes.
	// They live in a directory apart from prompts/ and are not listed as review modes.
	//go:embed partials/*.md
	partialFiles embed.FS

	// Templates are the HTML templates.
	//go:embed templates/*.html
	Templates embed.FS

	// StaticFiles holds the static files served to the browser, such as JavaScript.
	//go:embed static
	StaticFiles embed.FS
)

// ExcerptStyle is how a finding's quotation (excerpt / suggestion) is shown on screen.
//
// What a quote contains differs by mode. For code it is source code read in monospace,
// for novel / article it is Japanese prose. In the same style, a quote from a manuscript
// looks like code.
type ExcerptStyle string

const (
	// ExcerptProse quotes as prose (body font, light background).
	ExcerptProse ExcerptStyle = "prose"
	// ExcerptCode quotes as source code (monospace, dark background).
	ExcerptCode ExcerptStyle = "code"
)

// ModeMetadata is a mode's description, written in the front matter at the top of the prompt.
// As in the sibling apps, the description lives in the prompt itself.
//
// The screen keeps no list of its own because adding a mode only takes placing
// prompts/<mode>.md. With the description in a separate file, nobody would notice when
// whoever added a mode forgot to describe it, and the choices would just keep growing.
type ModeMetadata struct {
	// Label is the name shown as a choice. The key (file name) is in Latin letters, so the
	// Japanese display name is held separately.
	Label string `yaml:"label"`
	// Direction is a one-line description of what the mode reviews.
	Direction string `yaml:"direction"`
	// UseWhen says for what kind of subject the mode is chosen.
	UseWhen string `yaml:"use_when"`
	// Excerpt is how a finding's quote is shown on screen. Empty means ExcerptProse.
	//
	// Whoever adds a mode declares it on the prompt side. If the screen held a list such as
	// "monospace for code", adding a mode would no longer be just placing
	// prompts/<mode>.md.
	//
	// The default is prose because manuscripts are this app's main subject. A mode that
	// forgot to declare it does less harm shown as prose than shown as code.
	Excerpt ExcerptStyle `yaml:"excerpt"`
}

// Mode is one mode offered on the form.
type Mode struct {
	// Key is the file name of prompts/<key>.md and is the value passed to the worker as is.
	Key string
	ModeMetadata
}

// DisplayName is the name shown as a choice.
//
// A prompt without front matter is shown by its key, so the choice itself never disappears.
// A working mode vanishing from the screen because its description was forgotten would be worse.
func (m Mode) DisplayName() string {
	if m.Label != "" {
		return m.Label
	}
	return m.Key
}

// ExcerptKind returns how the mode's quotes are shown.
func (m Mode) ExcerptKind() ExcerptStyle {
	if m.Excerpt == "" {
		return ExcerptProse
	}
	return m.Excerpt
}

// promptSet is the prompt bodies and the mode information built from their front matter.
type promptSet struct {
	bodies map[string]string
	modes  map[string]Mode
}

// loadModes parses the embedded prompt bodies and their front matter once, on the first
// call. The bodies (LoadPrompts) and the mode information (AvailableModes) are separate
// entry points, but they come from the same directory.
//
// The only possible failures, such as front matter syntax or an excerpt value, give the
// same result every time, so the error is cached too and never retried.
//
// The returned maps are shared between callers. Do not modify them.
var loadModes = sync.OnceValues(func() (promptSet, error) {
	raw, err := resource.Load(promptFiles, promptDir)
	if err != nil {
		return promptSet{}, err
	}

	bodies, fronts := frontmatter.SplitMap(raw)

	// Not ignored silently. If a mistyped description just came out blank, nobody would
	// notice until opening the screen.
	metas, err := frontmatter.DecodeMap[ModeMetadata](fronts, yaml.Unmarshal)
	if err != nil {
		return promptSet{}, fmt.Errorf("cannot read the front matter of prompts: %w", err)
	}

	modes := make(map[string]Mode, len(bodies))
	for key := range bodies {
		meta := metas[key]

		// A misspelling fails startup. Falling back to the default at run time would keep a
		// mode meant to be shown as code silently displayed as prose.
		switch meta.Excerpt {
		case "", ExcerptProse, ExcerptCode:
		default:
			return promptSet{}, fmt.Errorf("prompt %q has an invalid excerpt: %q (prose or code)", key, meta.Excerpt)
		}

		modes[key] = Mode{Key: key, ModeMetadata: meta}
	}
	return promptSet{bodies: bodies, modes: modes}, nil
})

// LoadPrompts loads only the bodies of the embedded prompts.
//
// The front matter is a description, not part of the prompt, so it is dropped here.
// Passed on, the YAML would end up at the top of the instructions.
func LoadPrompts() (map[string]string, error) {
	set, err := loadModes()
	if err != nil {
		return nil, err
	}
	return maps.Clone(set.bodies), nil
}

// PromptTemplates returns the modes' prompt bodies and the shared fragments as one template set.
//
// The fragments go into the same set under "_"-prefixed template names so the bodies can
// reference them with {{template "_finding_policy" .}}. They stopped being pasted in as
// strings so that trimming the trailing newline is left to the library
// (prompts.WithTrimPartials). Fragments are inserted in the middle of bullet lists, and a
// leftover trailing newline splits the list at that point. Referenced as templates, the
// fragments can also use conditionals and data.
//
// Fragments are treated as go-prompt-kit partials, so they do not appear in AvailableModes.
func PromptTemplates() (map[string]string, error) {
	bodies, err := LoadPrompts()
	if err != nil {
		return nil, err
	}

	entries, err := partialFiles.ReadDir(partialDir)
	if err != nil {
		return nil, fmt.Errorf("failed to list the shared templates: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		b, err := partialFiles.ReadFile(partialDir + "/" + name)
		if err != nil {
			return nil, fmt.Errorf("failed to read shared template '%s': %w", name, err)
		}
		bodies[partialPrefix+strings.TrimSuffix(name, ".md")] = string(b)
	}
	return bodies, nil
}

// AvailableModes returns the modes available from the embedded review prompts, in key order.
//
// The order is fixed because, if the map's iteration order became the order of the
// choices, it would change on every render.
func AvailableModes() ([]Mode, error) {
	set, err := loadModes()
	if err != nil {
		return nil, err
	}

	modes := make([]Mode, 0, len(set.modes))
	for _, mode := range set.modes {
		modes = append(modes, mode)
	}

	sort.Slice(modes, func(i, j int) bool { return modes[i].Key < modes[j].Key })
	return modes, nil
}

// IsValidMode reports whether a prompt file exists for the given mode name.
func IsValidMode(mode string) bool {
	set, err := loadModes()
	if err != nil {
		slog.Error("failed to load prompts for validation", "error", err)
		return false
	}

	_, ok := set.modes[mode]
	return ok
}

// ExcerptStyleFor returns how a mode's quotes are shown.
//
// An unknown mode is not an error. The caller is the history detail page, and the history
// keeps the names of modes run in the past as they were. Being unable to open a past
// review after a prompt was deleted or renamed would be too high a price for a display concern.
func ExcerptStyleFor(mode string) ExcerptStyle {
	set, err := loadModes()
	if err != nil {
		return ExcerptProse
	}

	m, ok := set.modes[mode]
	if !ok {
		return ExcerptProse
	}
	return m.ExcerptKind()
}
