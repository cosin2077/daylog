package journal

import (
	"testing"
	"time"
)

func continuityFixture(at time.Time) Report {
	r := Report{SchemaVersion: 2, AppSource: "window_server", BatchID: "fixture", Start: at, End: at.Add(6 * time.Minute)}
	for _, item := range []struct {
		task, app string
		seconds   int
	}{{"a", "editor", 0}, {"a", "browser", 60}, {"b", "wechat", 120}, {"b", "wechat", 180}, {"a", "editor", 240}, {"a", "editor", 300}} {
		r.TaskObservations = append(r.TaskObservations, TaskObservation{At: at.Add(time.Duration(item.seconds) * time.Second), TaskID: item.task, App: item.app, MaxGapSeconds: 120})
	}
	r.Cards = []Card{
		{TaskID: "a", Title: "修复登录", Category: "写代码", Confidence: "medium", Start: at, End: at.Add(5 * time.Minute), Spans: []TimeSpan{{Start: at, End: at.Add(time.Minute)}, {Start: at.Add(4 * time.Minute), End: at.Add(5 * time.Minute)}}},
		{TaskID: "b", Title: "回复消息", Category: "聊天沟通", Confidence: "medium", Start: at.Add(2 * time.Minute), End: at.Add(3 * time.Minute), Spans: []TimeSpan{{Start: at.Add(2 * time.Minute), End: at.Add(3 * time.Minute)}}},
	}
	return r
}

func TestContinuitySeparatesToolSwitchTaskSwitchAndFragmentTime(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	r := continuityFixture(at)
	c := buildContinuity(r.Start, r.End, []Report{r})
	if c.TaskSwitches != 2 || c.AppSwitches != 3 || c.BriefReturns != 1 || c.ComparablePairs != 5 {
		t.Fatalf("incorrect switches: %+v", c)
	}
	if c.LongestSeconds != 60 || c.Runs != 3 || len(c.Fragments) != 1 || c.FragmentSeconds != 60 {
		t.Fatalf("transition gaps counted as time: %+v", c)
	}
	f := c.Fragments[0]
	if f.Title != "回复消息" || f.FromTitle != "修复登录" || !f.Returned || !f.NextAt.Equal(at.Add(4*time.Minute)) || len(c.FragmentDestinations) != 1 {
		t.Fatalf("missing destination or return evidence: %+v", f)
	}
	clipped := buildContinuity(at.Add(2*time.Minute), r.End, []Report{r})
	if len(clipped.Fragments) != 0 || clipped.BriefReturns != 0 {
		t.Fatal("invented a departure outside query range")
	}
}

func TestContinuityKeepsShortVisitWithoutReturn(t *testing.T) {
	at := time.Now()
	r := continuityFixture(at)
	r.Cards = append(r.Cards, Card{TaskID: "c", Title: "看推荐流", Category: "看内容"})
	r.TaskObservations[4].TaskID = "c"
	r.TaskObservations[5].TaskID = "c"
	c := buildContinuity(r.Start, r.End, []Report{r})
	if len(c.Fragments) != 1 || c.Fragments[0].Returned || c.BriefReturns != 0 || c.Fragments[0].NextTitle != "看推荐流" {
		t.Fatalf("lost A-B-C short visit: %+v", c)
	}
}

func TestContinuityDoesNotBridgeUnknownLongGapOrConflictingSamples(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	for _, mode := range []string{"unknown", "gap", "conflict", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			r := continuityFixture(at)
			switch mode {
			case "unknown":
				r.TaskObservations[3].TaskID = ""
			case "gap":
				r.TaskObservations[3].At = at.Add(20 * time.Minute)
				r.TaskObservations = r.TaskObservations[:4]
				r.End = at.Add(21 * time.Minute)
			case "conflict":
				p := r.TaskObservations[3]
				p.TaskID = "a"
				r.TaskObservations = append(r.TaskObservations, p)
			case "legacy":
				r.TaskObservations = nil
			}
			c := buildContinuity(r.Start, r.End, []Report{r})
			if c.BriefReturns != 0 || len(c.Fragments) != 0 {
				t.Fatalf("bridged unavailable evidence: %+v", c)
			}
			if mode == "legacy" && (c.MissingObservationBatches != 1 || c.ComparablePairs != 0) {
				t.Fatal("legacy reported as no switches")
			}
		})
	}
}

func TestContinuityMergesTouchingTaskSpansAcrossBatchesButNotDays(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	r := continuityFixture(at)
	r.Cards = r.Cards[:1]
	r.Cards[0].Spans = []TimeSpan{{Start: at, End: at.Add(5 * time.Minute)}}
	r2 := r
	r2.Start = at.Add(5 * time.Minute)
	r2.End = at.Add(10 * time.Minute)
	r2.Cards = []Card{{TaskID: "a", Title: "修复登录", Spans: []TimeSpan{{Start: r2.Start, End: r2.End}}}}
	c := buildContinuity(at, r2.End, []Report{r2, r})
	if c.LongestSeconds != 600 {
		t.Fatalf("lost cross-batch continuity: %+v", c)
	}
	r2.Cards[0].TaskID = "b"
	r2.Cards[0].Spans[0].Start = at.Add(4 * time.Minute)
	c = buildContinuity(at, r2.End, []Report{r, r2})
	if c.LongestSeconds != 0 {
		t.Fatal("conflicting task spans claimed continuous work")
	}
	midnight := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	r.Start = midnight.Add(-time.Minute)
	r.End = midnight.Add(time.Minute)
	r.Cards[0].Spans = []TimeSpan{{Start: r.Start, End: r.End}}
	c = buildContinuity(r.Start, r.End, []Report{r})
	if c.LongestSeconds != 60 || c.Runs != 2 {
		t.Fatal("merged across midnight")
	}
}

func TestV2RetainsSingleFrameDetourWithoutInventingDuration(t *testing.T) {
	s := testStore(t)
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	b := v2Batch(t, s, at)
	b.AppSource = "window_server"
	f := b.Frames[0]
	f.ID = ID(at.Add(120 * time.Second))
	f.At = at.Add(120 * time.Second)
	f.App = "wechat"
	b.Frames = append(b.Frames, f)
	b.Observations = []Observation{
		{At: at, App: "editor", State: "active"},
		{At: at, App: "editor", State: "sampled", FrameID: b.Frames[0].ID},
		{At: at.Add(90 * time.Second), App: "editor", State: "sampled", FrameID: b.Frames[0].ID},
		{At: f.At, App: f.App, State: "active"},
		{At: f.At, App: f.App, State: "sampled", FrameID: f.ID},
		{At: at.Add(140 * time.Second), App: "editor", State: "active"},
		{At: at.Add(150 * time.Second), App: "editor", State: "sampled", FrameID: b.Frames[0].ID},
		{At: at.Add(240 * time.Second), App: "editor", State: "sampled", FrameID: b.Frames[0].ID},
	}
	in := v2Input(b)
	in.Segments[0].Category = "写代码"
	in.Segments = append(in.Segments, Segment{Title: "回复项目消息", Summary: "查看群聊消息。", Category: "聊天沟通", Confidence: "medium", Actions: []string{}, Uncertainties: []string{}, Evidence: []string{f.ID}})
	r, err := deriveReport(b, in, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	c := buildContinuity(b.Start, b.End, []Report{r})
	if len(c.Fragments) != 1 || c.FragmentSeconds != 0 || c.TaskSwitches != 2 || c.LongestSeconds != 90 {
		t.Fatalf("single frame lost or charged duration: %+v", c)
	}
	in.Segments[1].Confidence = "low"
	r, err = deriveReport(b, in, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	c = buildContinuity(b.Start, b.End, []Report{r})
	if len(c.Fragments) != 0 || c.TaskSwitches != 0 {
		t.Fatal("low confidence treated as known detour")
	}
}

func TestPeriodKeepsLegacyClassificationWithoutInventingSpecificActivities(t *testing.T) {
	at := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	r := continuityFixture(at)
	r.Cards[0].Category = "工作"
	r.Cards[0].Uncertainties = []string{"示例限制"}
	r.TaskObservations = nil
	p := buildPeriod("day", at, []Report{r}, nil, Batch{})
	if p.Activities[0].Category != "未细分" || p.Activities[0].LegacyCategory != "工作" || len(p.Activities[0].Uncertainties) != 1 || r.Cards[0].Category != "工作" {
		t.Fatal("rewrote or guessed historical classification")
	}
	if p.Continuity.ComparablePairs != 0 || p.Continuity.MissingObservationBatches != 1 {
		t.Fatal("invented historical transitions")
	}
}
