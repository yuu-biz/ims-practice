package html

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/yuu-biz/ims-practice/slides/internal/deck"
	"github.com/yuu-biz/ims-practice/slides/internal/layout"
)

const sceneSource = "---\ntitle: T\nfooter: F\nlang: en\nformat: visual\n---\n\n" +
	"```ims-model\n" +
	"nodes:\n" +
	"  - {id: a, label: A, at: [0, 0]}\n" +
	"  - {id: b, label: B, at: [1, 0]}\n" +
	"events:\n" +
	"  - {id: req, msg: REQ, from: a, to: b, proto: SIP, fields: [{Request-URI: \"`sip:b` </script>\"}]}\n" +
	"  - {id: ans, msg: 200 OK, from: b, to: a, proto: SIP}\n" +
	"```\n\n---\n\n# T\n\n---\n\n## A scene\n\n" +
	"```ims-scene\n" +
	"id: flow\n" +
	"steps:\n" +
	"  - {id: send, title: Send, show: [req], text: A sends.}\n" +
	"  - {id: answer, title: Answer, show: [ans], zoom: [a], text: B answers.}\n" +
	"```\n\nThe longer explanation, [with a link](../../../docs/guide/en/x.md).\n"

func buildScene(t *testing.T, o Options) string {
	t.Helper()
	d, err := deck.Parse("scene.md", sceneSource)
	if err != nil {
		t.Fatal(err)
	}
	pages, problems := layout.Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var out bytes.Buffer
	if err := WriteWith(&out, d, pages, o); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestStepsAreInTheData makes sure that the page carries the complete
// state of every step, which the script draws from: an address with a
// step opens that state without stepping to it.
func TestStepsAreInTheData(t *testing.T) {
	page := buildScene(t, Options{})
	m := regexp.MustCompile(`(?s)<script type="application/json" id="data">(.*?)</script>`).FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no data")
	}
	var data struct {
		Scenes []sceneData
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(m[1], `<\/`, "</")), &data); err != nil {
		t.Fatal(err)
	}
	if len(data.Scenes) != 2 || data.Scenes[0].ID != "1" || data.Scenes[1].ID != "flow" {
		t.Fatalf("scenes = %+v", data.Scenes)
	}
	steps := data.Scenes[1].Steps
	if len(steps) != 2 || strings.Join(steps[1].Events, " ") != "req ans" || steps[1].Active != "ans" {
		t.Errorf("steps = %+v", steps)
	}
	if steps[1].Camera[2] >= steps[0].Camera[2] {
		t.Error("the step that zooms has no smaller view")
	}
	if strings.Contains(m[1], "</script>") {
		t.Error("the data ends its script element early")
	}
}

func TestSceneMarkup(t *testing.T) {
	page := buildScene(t, Options{Repository: "https://example.org/tree", Translation: "../ja/scene.html", TranslationLang: "ja"})
	for _, want := range []string{
		`<section class="slide scene wide" id="slide-flow"`,
		`<g class="event sip" data-id="req" data-nodes="a b" data-state="active">`,
		`<g class="event sip" data-id="ans" data-nodes="b a" data-state="off">`,
		`<div class="step" data-step="answer" hidden>`,
		`<a href="https://example.org/tree/docs/guide/en/x.md">`,
		`<a id="translation" href="../ja/scene.html" hreflang="ja" lang="ja">日本語</a>`,
		`<article id="reading" hidden`,
		`<figure class="checkpoint">`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
	if regexp.MustCompile(`(?i)<(script|link|img|iframe)[^>]+(src|href)=`).MatchString(page) {
		t.Error("the page loads a file")
	}
}

// TestLadderBandCoversTheRoomAboveTheHeads makes sure that the band
// behind the heads of a ladder reaches above them, into the room that a
// stage of another shape than the camera adds there: a row that the
// view has scrolled past does not show above the heads. The heads say
// the role of each node as well.
func TestLadderBandCoversTheRoomAboveTheHeads(t *testing.T) {
	source := strings.Replace(sceneSource, "id: flow\n", "id: flow\nview: ladder\n", 1)
	source = strings.Replace(source, "{id: a, label: A, at: [0, 0]}", "{id: a, label: A, role: sends, at: [0, 0]}", 1)
	d, err := deck.Parse("ladder.md", source)
	if err != nil {
		t.Fatal(err)
	}
	pages, problems := layout.Deck(d)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	var out bytes.Buffer
	if err := WriteWith(&out, d, pages, Options{}); err != nil {
		t.Fatal(err)
	}
	band := regexp.MustCompile(`<rect class="band" x="[^"]*" y="(-?[0-9.]+)"`).FindStringSubmatch(out.String())
	if band == nil || !strings.HasPrefix(band[1], "-") {
		t.Errorf("the band starts at %v, not above the heads", band)
	}
	if !strings.Contains(out.String(), `<text class="role"`) || !strings.Contains(out.String(), ">sends</text>") {
		t.Error("the head of a has no role")
	}
}
