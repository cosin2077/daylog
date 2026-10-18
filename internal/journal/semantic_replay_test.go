package journal

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Annotated interpretation replay verifies identity/category aggregation and
// duration, not a claim that a vision model will always produce these answers.
func TestSemanticInterpretationReplays(t *testing.T) {
	raw, err := os.ReadFile("testdata/semantic-replays.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name              string
		Evidence          []string
		Segments          []Segment
		Tasks, Categories int
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
			b := Batch{ID: ID(at), SchemaVersion: 2, AppSource: "fixture", Start: at, End: at.Add(time.Duration(len(tc.Evidence)) * 90 * time.Second), MaxGapSeconds: 180}
			for i := range tc.Evidence {
				id := "f" + string(rune('0'+i))
				point := at.Add(time.Duration(i) * 90 * time.Second)
				b.Frames = append(b.Frames, Frame{ID: id, At: point, App: "browser"})
				b.Observations = append(b.Observations, Observation{At: point, FrameID: id, App: "browser", State: "sampled"})
			}
			input, _ := json.Marshal(Interpretation{Segments: tc.Segments})
			in, err := decodeInterpretation(b, input)
			if err != nil {
				t.Fatal(err)
			}
			r, err := deriveReport(b, in, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			goals, categories := map[string]bool{}, map[string]bool{}
			for _, c := range r.Cards {
				goals[c.TaskID] = true
				categories[c.Category] = true
			}
			if len(goals) != tc.Tasks || len(categories) != tc.Categories {
				t.Fatal("identity or category lost", r.Cards)
			}
			if tc.Name == "same_goal_different_actions" {
				v := BuildOverview(at, []Report{r}, nil, Batch{}).Views[0]
				count := 0
				for _, c := range v.Categories {
					if c.Count > 0 {
						count++
					}
				}
				if len(v.Tasks) != 1 || v.Tasks[0].Title != "排查缓存过期边界" || count != 3 {
					t.Fatal(v.Tasks, v.Categories)
				}
			}
			if tc.Name == "low_quality_and_blank" {
				for _, c := range r.Cards {
					if c.EstimatedSeconds != 0 {
						t.Fatal("low confidence was timed")
					}
				}
			}
		})
	}
}

func TestLocalTaskLinksAreBoundedAndOptional(t *testing.T) {
	s := testStore(t)
	b := v2Batch(t, s, time.Now())
	in := v2Input(b)
	for _, kind := range []string{"invalid_key", "title_without_key", "low", "conflict", "foreign_id"} {
		t.Run(kind, func(t *testing.T) {
			g := in.Segments[0]
			g.TaskKey = "cache"
			g.TaskTitle = "缓存问题"
			switch kind {
			case "invalid_key":
				g.TaskKey = "../escape"
			case "title_without_key":
				g.TaskKey = ""
			case "low":
				g.Confidence = "low"
			case "conflict":
				g.TaskID = "existing"
			case "foreign_id":
				g.TaskKey = ""
				g.TaskTitle = ""
				g.TaskID = "other_batch_local_cache"
			}
			if _, err := deriveReport(b, Interpretation{SchemaVersion: 2, BatchID: b.ID, Segments: []Segment{g}}, ""); err == nil {
				t.Fatal("unsupported association accepted")
			}
		})
	}
	if _, err := deriveReport(b, in, ""); err != nil {
		t.Fatal("old minimal schema broken", err)
	}
}
