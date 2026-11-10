package adkagent

import (
	"slices"
	"testing"

	"google.golang.org/genai"

	"github.com/shouni/go-review-kit/review"
)

// The schema's enumerated values are built from the review package's definitions. If the two
// disagree, the model returns a value valid by the schema and is rejected on decode.
func TestSchemaEnumsMatchDomain(t *testing.T) {
	t.Parallel()

	schema := reportSchema()

	verdict, ok := schema.Properties["verdict"]
	if !ok {
		t.Fatal("the schema has no verdict")
	}
	if got, want := verdict.Properties["decision"].Enum, review.DecisionStrings(); !slices.Equal(got, want) {
		t.Errorf("decision enum = %v, want %v", got, want)
	}

	findings, ok := schema.Properties["findings"]
	if !ok {
		t.Fatal("the schema has no findings")
	}
	if got, want := findings.Items.Properties["severity"].Enum, review.SeverityStrings(); !slices.Equal(got, want) {
		t.Errorf("severity enum = %v, want %v", got, want)
	}
}

// The schema's required fields must match the shape review.Report's decoding assumes.
func TestSchemaShape(t *testing.T) {
	t.Parallel()

	schema := reportSchema()

	if schema.Type != genai.TypeObject {
		t.Fatalf("top-level type = %v", schema.Type)
	}

	for _, key := range []string{"title", "summary", "verdict", "findings"} {
		if _, ok := schema.Properties[key]; !ok {
			t.Errorf("the schema has no %s", key)
		}
		if !slices.Contains(schema.Required, key) {
			t.Errorf("%s is not required", key)
		}
	}

	findings := schema.Properties["findings"]
	if findings.Type != genai.TypeArray || findings.Items == nil {
		t.Fatal("findings is not defined as an array")
	}

	for _, key := range []string{"severity", "file", "excerpt", "message"} {
		if !slices.Contains(findings.Items.Required, key) {
			t.Errorf("findings[].%s is not required", key)
		}
	}

	// The history list puts the title into one column untruncated. Since the screen does not cut
	// it, this is the only place that asks for a length (verdict_format.md's Japanese states the
	// target, this is the safeguard).
	if title := schema.Properties["title"]; title.MaxLength == nil {
		t.Error("title has no MaxLength (a long title breaks the history table)")
	} else if *title.MaxLength != maxTitleLength {
		t.Errorf("title MaxLength = %d, want %d", *title.MaxLength, maxTitleLength)
	}

	// ★ Hitting the 64Ki output tokens loses everything, including JSON that was correct so far.
	// If the schema says nothing about length, to the model it is unlimited.
	if findings.MaxItems == nil {
		t.Error("findings has no MaxItems (hitting the output limit loses everything)")
	} else if *findings.MaxItems != maxFindings {
		t.Errorf("findings MaxItems = %d, want %d", *findings.MaxItems, maxFindings)
	}

	// What balloons is usually the quote. A cut run's tail was filled with one code fragment repeated.
	excerpt := findings.Items.Properties["excerpt"]
	if excerpt.MaxLength == nil {
		t.Error("excerpt has no MaxLength")
	} else if *excerpt.MaxLength != maxExcerptLength {
		t.Errorf("excerpt MaxLength = %d, want %d", *excerpt.MaxLength, maxExcerptLength)
	}

	// evidence is included only here. This reviewer actually investigates the working directory,
	// so having it declare "where it looked to decide" means something (see the comment in schema.go).
	if _, ok := findings.Items.Properties["evidence"]; !ok {
		t.Error("the agent schema needs evidence")
	}
}
