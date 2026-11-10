package adapters

import (
	"fmt"

	"github.com/shouni/go-prompt-kit/prompts"
	"github.com/shouni/go-review-kit/review"

	"github.com/shouni/adk-review/assets"
)

// reviewData is the data passed to the review prompt template.
//
// The shared fragments (finding policy, output format) are not passed as strings; the body
// references them as partials of the template set.
type reviewData struct {
	DiffContent string
}

// promptBuilder is the interface for producing a formatted prompt
type promptBuilder interface {
	Build(mode string, data any) (string, error)
}

// PromptAdapter builds the review prompt for a mode.
type PromptAdapter struct {
	builder promptBuilder
}

var _ review.PromptGenerator = (*PromptAdapter)(nil)

// NewPromptAdapter builds the Builder from the dynamically loaded templates.
func NewPromptAdapter() (*PromptAdapter, error) {
	templates, err := assets.PromptTemplates()
	if err != nil {
		return nil, fmt.Errorf("failed to load the review templates: %w", err)
	}

	// WithTrimPartials is set because fragments are inserted in the middle of bullet lists.
	// A leftover trailing newline puts a blank line at the insertion point, and the
	// mode-specific policy that follows reads as a separate list.
	builder, err := prompts.NewBuilder(templates, prompts.WithTrimPartials())
	if err != nil {
		return nil, fmt.Errorf("failed to build the review builder: %w", err)
	}

	return &PromptAdapter{builder: builder}, nil
}

// Generate produces the review prompt.
func (pa *PromptAdapter) Generate(mode, diff string) (string, error) {
	prompt, err := pa.builder.Build(mode, reviewData{DiffContent: diff})
	if err != nil {
		return "", fmt.Errorf("failed to execute the review template: %w", err)
	}
	return prompt, nil
}
