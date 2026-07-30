package engine

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode"

	xhtml "golang.org/x/net/html"
)

const (
	mozillaCorpusDir    = "../../testdata/readability-js/test/test-pages"
	mozillaGatePath     = "../../testdata/mozilla/gate.json"
	mozillaCommit       = "ab4027a8b37669745016869a37a504727992b2ba"
	mozillaFixtures     = 130
	mozillaSyntheticURL = "http://fakehost/test/page.html"
)

type mozillaScore struct {
	Precision float64         `json:"precision"`
	Recall    float64         `json:"recall"`
	F1        float64         `json:"f1"`
	Metadata  map[string]bool `json:"metadata,omitempty"`
}

type mozillaAggregateGate struct {
	PrecisionMin        float64 `json:"precision_min"`
	RecallMin           float64 `json:"recall_min"`
	F1Min               float64 `json:"f1_min"`
	ExtractionErrorsMax int     `json:"extraction_errors_max"`
}

type mozillaMetadataGate struct {
	MatchesMin int `json:"matches_min"`
	Compared   int `json:"compared"`
}

type mozillaGate struct {
	SchemaVersion int                            `json:"schema_version"`
	MozillaCommit string                         `json:"mozilla_commit"`
	FixtureCount  int                            `json:"fixture_count"`
	Aggregate     mozillaAggregateGate           `json:"aggregate"`
	Metadata      map[string]mozillaMetadataGate `json:"metadata"`
	Fixtures      map[string]mozillaScore        `json:"fixtures"`
}

type mozillaMetadata struct {
	Title         string
	Byline        string
	Excerpt       string
	SiteName      string
	PublishedTime string
	Lang          string
	present       map[string]bool
}

type mozillaFixtureResult struct {
	name           string
	score          mozillaScore
	expectedWords  map[string]int
	actualWords    map[string]int
	metadataMatch  map[string]bool
	metadataActual map[string]string
	metadataWant   map[string]string
	err            error
}

// mozillaWords documents this lane's comparison policy. Text is lower-cased;
// Unicode letters, numbers, and combining marks form words; punctuation and
// all whitespace are boundaries. Scores use multiset token counts, so repeated
// words count and HTML serialization or attribute order cannot affect results.
func mozillaWords(text string) map[string]int {
	words := make(map[string]int)
	var token []rune
	flush := func() {
		if len(token) != 0 {
			words[string(token)]++
			token = token[:0]
		}
	}
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) || unicode.IsMark(r) {
			token = append(token, unicode.ToLower(r))
		} else {
			flush()
		}
	}
	flush()
	return words
}

func mozillaTextFromHTML(source []byte) (string, error) {
	root, err := xhtml.Parse(strings.NewReader(string(source)))
	if err != nil {
		return "", err
	}
	var text strings.Builder
	var walk func(*xhtml.Node, bool)
	walk = func(n *xhtml.Node, hidden bool) {
		if n.Type == xhtml.ElementNode {
			switch n.Data {
			case "script", "style", "template", "noscript":
				hidden = true
			}
		}
		if n.Type == xhtml.TextNode && !hidden {
			text.WriteByte(' ')
			text.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child, hidden)
		}
	}
	walk(root, false)
	return text.String(), nil
}

func mozillaMultisetScore(expected, actual map[string]int) (mozillaScore, int, int, int) {
	matched, expectedTotal, actualTotal := 0, 0, 0
	for word, count := range expected {
		expectedTotal += count
		if actual[word] < count {
			matched += actual[word]
		} else {
			matched += count
		}
	}
	for _, count := range actual {
		actualTotal += count
	}
	precision, recall := 1.0, 1.0
	if actualTotal != 0 {
		precision = float64(matched) / float64(actualTotal)
	} else if expectedTotal != 0 {
		precision = 0
	}
	if expectedTotal != 0 {
		recall = float64(matched) / float64(expectedTotal)
	} else if actualTotal != 0 {
		recall = 0
	}
	f1 := 0.0
	if precision+recall != 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return mozillaScore{Precision: precision, Recall: recall, F1: f1}, matched, expectedTotal, actualTotal
}

func normalizeMozillaMetadata(value string) string {
	return strings.Join(strings.Fields(stdhtml.UnescapeString(value)), " ")
}

func readMozillaMetadata(path string) (mozillaMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mozillaMetadata{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return mozillaMetadata{}, err
	}
	value := func(key string) string {
		entry, ok := raw[key]
		if !ok || string(entry) == "null" {
			return ""
		}
		var result string
		_ = json.Unmarshal(entry, &result)
		return result
	}
	present := make(map[string]bool)
	for _, key := range []string{"title", "byline", "excerpt", "siteName", "publishedTime", "lang"} {
		_, present[key] = raw[key]
	}
	return mozillaMetadata{Title: value("title"), Byline: value("byline"), Excerpt: value("excerpt"), SiteName: value("siteName"), PublishedTime: value("publishedTime"), Lang: value("lang"), present: present}, nil
}

func TestMozillaMetricHelpers(t *testing.T) {
	text, err := mozillaTextFromHTML([]byte(`<article data-x="1"><p>Hello,&nbsp;WORLD!</p><script>ignored words</script><p>Café 42</p></article>`))
	if err != nil {
		t.Fatal(err)
	}
	words := mozillaWords(text)
	for _, word := range []string{"hello", "world", "café", "42"} {
		if words[word] != 1 {
			t.Errorf("word %q count = %d, want 1", word, words[word])
		}
	}
	if words["ignored"] != 0 {
		t.Error("script text was included")
	}
	score, matched, expected, actual := mozillaMultisetScore(map[string]int{"a": 2, "b": 1}, map[string]int{"a": 1, "c": 1})
	if matched != 1 || expected != 3 || actual != 2 || math.Abs(score.Precision-.5) > 1e-12 || math.Abs(score.Recall-1.0/3) > 1e-12 || math.Abs(score.F1-.4) > 1e-12 {
		t.Fatalf("unexpected score: %#v matched=%d expected=%d actual=%d", score, matched, expected, actual)
	}
	if got := normalizeMozillaMetadata("  A&nbsp;  B\nC  "); got != "A B C" {
		t.Fatalf("normalized metadata = %q", got)
	}
}

func TestMozillaReadabilityCompatibility(t *testing.T) {
	if _, err := os.Stat(mozillaCorpusDir); err != nil {
		if os.IsNotExist(err) {
			t.Skip("Mozilla Readability corpus is not initialized; run: git submodule update --init --recursive")
		}
		t.Fatal(err)
	}

	gateData, err := os.ReadFile(mozillaGatePath)
	if err != nil {
		t.Fatal(err)
	}
	var gate mozillaGate
	if err := json.Unmarshal(gateData, &gate); err != nil {
		t.Fatalf("decode %s: %v", mozillaGatePath, err)
	}
	updatingGate := os.Getenv("UPDATE_MOZILLA_GATE") == "1"
	if gate.SchemaVersion != 1 {
		t.Fatalf("unsupported Mozilla gate schema version %d", gate.SchemaVersion)
	}
	if !updatingGate && (gate.MozillaCommit != mozillaCommit || gate.FixtureCount != mozillaFixtures) {
		t.Fatalf("gate provenance mismatch: commit=%q fixtures=%d", gate.MozillaCommit, gate.FixtureCount)
	}
	metadataFields := []string{"title", "byline", "excerpt", "siteName", "publishedTime", "lang"}
	if len(gate.Metadata) != len(metadataFields) {
		t.Fatalf("gate has %d metadata fields, want %d", len(gate.Metadata), len(metadataFields))
	}
	for _, field := range metadataFields {
		if requirement, ok := gate.Metadata[field]; !ok || requirement.Compared <= 0 {
			t.Fatalf("gate has no valid %q metadata requirement", field)
		}
	}

	entries, err := os.ReadDir(mozillaCorpusDir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	if len(names) != mozillaFixtures {
		t.Fatalf("found %d fixture directories, want %d at Mozilla commit %s", len(names), mozillaFixtures, mozillaCommit)
	}
	if !updatingGate {
		if len(gate.Fixtures) != mozillaFixtures {
			t.Fatalf("gate has %d fixture baselines, want %d", len(gate.Fixtures), mozillaFixtures)
		}
		for _, name := range names {
			baseline, ok := gate.Fixtures[name]
			if !ok {
				t.Fatalf("gate has no baseline for fixture %q", name)
			}
			if len(baseline.Metadata) != len(metadataFields) {
				t.Fatalf("gate fixture %q has %d metadata baselines, want %d", name, len(baseline.Metadata), len(metadataFields))
			}
		}
	}

	var results []mozillaFixtureResult
	totalMatched, totalExpected, totalActual, extractionErrors := 0, 0, 0, 0
	metadataMatches := make(map[string]int)
	metadataCompared := make(map[string]int)
	for _, name := range names {
		dir := filepath.Join(mozillaCorpusDir, name)
		source, sourceErr := os.ReadFile(filepath.Join(dir, "source.html"))
		expectedHTML, expectedErr := os.ReadFile(filepath.Join(dir, "expected.html"))
		metadata, metadataErr := readMozillaMetadata(filepath.Join(dir, "expected-metadata.json"))
		result := mozillaFixtureResult{name: name}
		if sourceErr != nil || expectedErr != nil || metadataErr != nil {
			result.err = fmt.Errorf("fixture files: source=%v expected=%v metadata=%v", sourceErr, expectedErr, metadataErr)
			extractionErrors++
			results = append(results, result)
			continue
		}
		expectedText, err := mozillaTextFromHTML(expectedHTML)
		if err != nil {
			result.err = fmt.Errorf("parse expected HTML: %w", err)
			extractionErrors++
			results = append(results, result)
			continue
		}
		result.expectedWords = mozillaWords(expectedText)
		result.metadataActual = map[string]string{"title": "", "byline": "", "excerpt": "", "siteName": "", "publishedTime": "", "lang": ""}
		result.metadataWant = map[string]string{"title": metadata.Title, "byline": metadata.Byline, "excerpt": metadata.Excerpt, "siteName": metadata.SiteName, "publishedTime": metadata.PublishedTime, "lang": metadata.Lang}
		doc, err := ExtractBytes(source, mozillaSyntheticURL, WithPageType(PageTypeArticle))
		if err != nil {
			result.err = fmt.Errorf("extract: %w", err)
			extractionErrors++
			_, _, expectedCount, _ := mozillaMultisetScore(result.expectedWords, nil)
			totalExpected += expectedCount
			compareMozillaMetadata(&result, metadata.present, metadataMatches, metadataCompared)
			results = append(results, result)
			continue
		}
		result.actualWords = mozillaWords(doc.Text)
		var total, expectedCount, actualCount int
		result.score, total, expectedCount, actualCount = mozillaMultisetScore(result.expectedWords, result.actualWords)
		totalMatched += total
		totalExpected += expectedCount
		totalActual += actualCount

		result.metadataActual = map[string]string{"title": doc.Title, "byline": doc.Author, "excerpt": doc.Description, "siteName": doc.SiteName, "publishedTime": doc.PublishedTime, "lang": doc.Language}
		compareMozillaMetadata(&result, metadata.present, metadataMatches, metadataCompared)
		results = append(results, result)
	}

	aggregate, _, _, _ := mozillaMultisetScoreFromTotals(totalMatched, totalExpected, totalActual)
	if updatingGate {
		writeMozillaGate(t, gate, aggregate, extractionErrors, metadataMatches, metadataCompared, results)
		return
	}

	failed := aggregate.Precision+1e-12 < gate.Aggregate.PrecisionMin || aggregate.Recall+1e-12 < gate.Aggregate.RecallMin || aggregate.F1+1e-12 < gate.Aggregate.F1Min || extractionErrors > gate.Aggregate.ExtractionErrorsMax
	for field, requirement := range gate.Metadata {
		if metadataCompared[field] != requirement.Compared || metadataMatches[field] < requirement.MatchesMin {
			failed = true
		}
	}
	if !failed {
		return
	}

	t.Errorf("Mozilla Readability article compatibility regression (required -> actual): precision %.4f -> %.6f; recall %.4f -> %.6f; F1 %.4f -> %.6f; extraction errors %d -> %d", gate.Aggregate.PrecisionMin, aggregate.Precision, gate.Aggregate.RecallMin, aggregate.Recall, gate.Aggregate.F1Min, aggregate.F1, gate.Aggregate.ExtractionErrorsMax, extractionErrors)
	fields := make([]string, 0, len(gate.Metadata))
	for field := range gate.Metadata {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		requirement := gate.Metadata[field]
		t.Logf("metadata %-13s required %d/%d, actual %d/%d", field, requirement.MatchesMin, requirement.Compared, metadataMatches[field], metadataCompared[field])
	}
	reportMozillaRegressions(t, results, gate)
	reportMozillaMetadataRegressions(t, results, gate)
}

func compareMozillaMetadata(result *mozillaFixtureResult, present map[string]bool, matches, compared map[string]int) {
	result.metadataMatch = make(map[string]bool)
	for _, field := range []string{"title", "byline", "excerpt", "siteName", "publishedTime", "lang"} {
		if !present[field] {
			continue
		}
		compared[field]++
		result.metadataMatch[field] = normalizeMozillaMetadata(result.metadataActual[field]) == normalizeMozillaMetadata(result.metadataWant[field])
		if result.metadataMatch[field] {
			matches[field]++
		}
	}
}

func mozillaMultisetScoreFromTotals(matched, expected, actual int) (mozillaScore, int, int, int) {
	precision, recall := 1.0, 1.0
	if actual != 0 {
		precision = float64(matched) / float64(actual)
	} else if expected != 0 {
		precision = 0
	}
	if expected != 0 {
		recall = float64(matched) / float64(expected)
	} else if actual != 0 {
		recall = 0
	}
	f1 := 0.0
	if precision+recall != 0 {
		f1 = 2 * precision * recall / (precision + recall)
	}
	return mozillaScore{Precision: precision, Recall: recall, F1: f1}, matched, expected, actual
}

func reportMozillaRegressions(t *testing.T, results []mozillaFixtureResult, gate mozillaGate) {
	t.Helper()
	regression := func(result mozillaFixtureResult) float64 {
		baseline := gate.Fixtures[result.name]
		return min(result.score.Precision-baseline.Precision, result.score.Recall-baseline.Recall, result.score.F1-baseline.F1)
	}
	sort.Slice(results, func(i, j int) bool {
		iDelta, jDelta := regression(results[i]), regression(results[j])
		if iDelta == jDelta {
			return results[i].score.F1 < results[j].score.F1
		}
		return iDelta < jDelta
	})
	limit := 10
	if len(results) < limit {
		limit = len(results)
	}
	t.Log("worst-regressing fixtures (bounded diagnostics):")
	for _, result := range results[:limit] {
		baseline := gate.Fixtures[result.name]
		if result.err != nil {
			t.Logf("  %s: error=%v (baseline F1 %.6f)", result.name, result.err, baseline.F1)
			continue
		}
		t.Logf("  %s: P=%.4f R=%.4f F1=%.4f deltas(P=%+.4f R=%+.4f F1=%+.4f) missing=[%s] extra=[%s]", result.name, result.score.Precision, result.score.Recall, result.score.F1, result.score.Precision-baseline.Precision, result.score.Recall-baseline.Recall, result.score.F1-baseline.F1, representativeMozillaWords(result.expectedWords, result.actualWords), representativeMozillaWords(result.actualWords, result.expectedWords))
		for field, matched := range result.metadataMatch {
			if !matched {
				t.Logf("    metadata %s: want %q got %q", field, boundedMozillaValue(result.metadataWant[field]), boundedMozillaValue(result.metadataActual[field]))
			}
		}
	}
}

func boundedMozillaValue(value string) string {
	const limit = 160
	value = normalizeMozillaMetadata(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

func reportMozillaMetadataRegressions(t *testing.T, results []mozillaFixtureResult, gate mozillaGate) {
	t.Helper()
	type regression struct {
		fixture string
		field   string
		want    string
		actual  string
	}
	var regressions []regression
	for _, result := range results {
		baseline := gate.Fixtures[result.name]
		for field, previouslyMatched := range baseline.Metadata {
			if previouslyMatched && !result.metadataMatch[field] {
				regressions = append(regressions, regression{fixture: result.name, field: field, want: result.metadataWant[field], actual: result.metadataActual[field]})
			}
		}
	}
	if len(regressions) == 0 {
		return
	}
	sort.Slice(regressions, func(i, j int) bool {
		if regressions[i].fixture == regressions[j].fixture {
			return regressions[i].field < regressions[j].field
		}
		return regressions[i].fixture < regressions[j].fixture
	})
	if len(regressions) > 10 {
		regressions = regressions[:10]
	}
	t.Log("metadata regressions from per-fixture baseline:")
	for _, regression := range regressions {
		t.Logf("  %s %s: want %q got %q", regression.fixture, regression.field, boundedMozillaValue(regression.want), boundedMozillaValue(regression.actual))
	}
}

func representativeMozillaWords(primary, other map[string]int) string {
	type item struct {
		word  string
		count int
	}
	var items []item
	for word, count := range primary {
		if delta := count - other[word]; delta > 0 {
			items = append(items, item{word: word, count: delta})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count == items[j].count {
			return items[i].word < items[j].word
		}
		return items[i].count > items[j].count
	})
	if len(items) > 12 {
		items = items[:12]
	}
	parts := make([]string, len(items))
	for i, item := range items {
		parts[i] = fmt.Sprintf("%s:%d", item.word, item.count)
	}
	return strings.Join(parts, " ")
}

func writeMozillaGate(t *testing.T, gate mozillaGate, aggregate mozillaScore, extractionErrors int, matches, compared map[string]int, results []mozillaFixtureResult) {
	t.Helper()
	floor4 := func(value float64) float64 { return math.Floor(value*10000) / 10000 }
	gate.SchemaVersion = 1
	gate.MozillaCommit = mozillaCommit
	gate.FixtureCount = mozillaFixtures
	gate.Aggregate = mozillaAggregateGate{PrecisionMin: floor4(aggregate.Precision), RecallMin: floor4(aggregate.Recall), F1Min: floor4(aggregate.F1), ExtractionErrorsMax: extractionErrors}
	gate.Metadata = make(map[string]mozillaMetadataGate)
	for field, count := range compared {
		gate.Metadata[field] = mozillaMetadataGate{MatchesMin: matches[field], Compared: count}
	}
	gate.Fixtures = make(map[string]mozillaScore)
	for _, result := range results {
		baseline := result.score
		baseline.Metadata = make(map[string]bool)
		for _, field := range []string{"title", "byline", "excerpt", "siteName", "publishedTime", "lang"} {
			baseline.Metadata[field] = result.metadataMatch[field]
		}
		gate.Fixtures[result.name] = baseline
	}
	data, err := json.MarshalIndent(gate, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(mozillaGatePath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("updated %s: P=%.6f R=%.6f F1=%.6f errors=%d", mozillaGatePath, aggregate.Precision, aggregate.Recall, aggregate.F1, extractionErrors)
}
