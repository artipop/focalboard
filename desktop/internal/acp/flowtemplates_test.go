package acp

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The board template and the seeded routes are two halves of one promise: open
// a fresh "My Project Tasks" board and the routes already point at columns that
// exist, with a "Workflow" property whose options name them. Nothing in the
// build enforces that — the template is JSON in the server module — so the test
// reads the template itself and compares.

const templateBoardTitle = "My Project Tasks"

type templateProperty struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Options []struct {
		ID    string `json:"id"`
		Value string `json:"value"`
	} `json:"options"`
}

type templateBoard struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Fields struct {
		CardProperties []templateProperty `json:"cardProperties"`
		Properties     struct {
			Columns []ColumnSpec `json:"acpColumns"`
			Flows   []FlowEntry  `json:"acpFlows"`
		} `json:"properties"`
	} `json:"fields"`
}

// readTemplateBoard finds the board template the seeded routes are written for.
func readTemplateBoard(t *testing.T) templateBoard {
	t.Helper()
	root := filepath.Join("..", "..", "..", "server", "assets", "templates-boardarchive")
	files, err := filepath.Glob(filepath.Join(root, "*", "board.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		// The desktop module is buildable on its own; without the server tree
		// there is nothing to compare against.
		t.Skipf("board templates not found under %s", root)
	}
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 1<<22)
		for sc.Scan() {
			var rec struct {
				Data templateBoard `json:"data"`
			}
			if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
				continue
			}
			if rec.Data.Type == "board" && rec.Data.Title == templateBoardTitle {
				return rec.Data
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
	}
	t.Fatalf("the %q template is gone from %s", templateBoardTitle, root)
	return templateBoard{}
}

func (b templateBoard) options(t *testing.T, property string) map[string]bool {
	t.Helper()
	for _, p := range b.Fields.CardProperties {
		if strings.EqualFold(p.Name, property) {
			out := make(map[string]bool, len(p.Options))
			for _, o := range p.Options {
				out[strings.ToLower(o.Value)] = true
			}
			return out
		}
	}
	t.Fatalf("the %q template has no %q property", templateBoardTitle, property)
	return nil
}

func TestTemplateFlowsMatchTheBoardTemplate(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	board := readTemplateBoard(t)
	flows := TemplateFlows(cfg)

	// Every stage must have somewhere to move a card to. A column that is not
	// an option of the board is a route that fails on its first transition.
	columns := board.options(t, cfg.TriggerProperty)
	for _, f := range flows {
		for _, n := range f.Nodes {
			if !columns[strings.ToLower(n.Column)] {
				t.Errorf("flow %q: the %q template has no %q column — add the option or rename the stage",
					f.Name, templateBoardTitle, n.Column)
			}
		}
	}

	// And the property a card picks its route with must offer exactly the
	// routes that exist: an option naming nothing does nothing.
	picks := board.options(t, "Workflow")
	names := make(map[string]bool, len(flows))
	for _, f := range flows {
		names[strings.ToLower(f.Name)] = true
		if !picks[strings.ToLower(f.Name)] {
			t.Errorf("flow %q is seeded but the template's Workflow property does not offer it", f.Name)
		}
	}
	for option := range picks {
		if !names[option] {
			t.Errorf("the template offers the workflow %q, which no seeded route answers to", option)
		}
	}
}

// The board brings its own automation, and the Go side is where the shipped
// routes are written. Nothing in the build ties the two together, so the test
// does: what the template carries must be what TemplateFlows says, bound to the
// template's own option ids.
func TestTemplateBoardCarriesTheShippedAutomation(t *testing.T) {
	cfg := DefaultConfig(t.TempDir())
	board := readTemplateBoard(t)
	columns := board.Fields.Properties.Columns
	flows := board.Fields.Properties.Flows

	// The three columns that do something, each bound to a real option.
	options := map[string]string{} // lowercased name → option id
	for _, p := range board.Fields.CardProperties {
		if strings.EqualFold(p.Name, cfg.TriggerProperty) {
			for _, o := range p.Options {
				options[strings.ToLower(o.Value)] = o.ID
			}
		}
	}
	wantActions := map[string]string{
		strings.ToLower(cfg.TriggerColumn): FlowActionAgent,
		strings.ToLower(cfg.DeployColumn):  FlowActionDeploy,
		strings.ToLower(cfg.TestColumn):    FlowActionTest,
	}
	if len(columns) != len(wantActions) {
		t.Fatalf("the template ships %d columns: %+v", len(columns), columns)
	}
	for _, c := range columns {
		lower := strings.ToLower(c.Column)
		if want := wantActions[lower]; want != c.Action {
			t.Errorf("column %q does %q, expected %q", c.Column, c.Action, want)
		}
		if c.OptionID == "" || c.OptionID != options[lower] {
			t.Errorf("column %q is not bound to the template's own option: %q", c.Column, c.OptionID)
		}
	}

	// The routes are the shipped ones, stage for stage, and every stage stands
	// on a column of this board.
	want := TemplateFlows(cfg)
	if len(flows) != len(want) {
		t.Fatalf("the template ships %d routes, TemplateFlows has %d", len(flows), len(want))
	}
	for i, f := range flows {
		if f.Name != want[i].Name || len(f.Nodes) != len(want[i].Nodes) || len(f.Edges) != len(want[i].Edges) {
			t.Fatalf("route %q differs from the shipped one: %+v", f.Name, f)
		}
		for j, n := range f.Nodes {
			if n.Column != want[i].Nodes[j].Column {
				t.Errorf("route %q stage %d: column %q, expected %q", f.Name, j+1, n.Column, want[i].Nodes[j].Column)
			}
			if n.OptionID != options[strings.ToLower(n.Column)] {
				t.Errorf("route %q stage %q is not bound to an option of the board", f.Name, n.Column)
			}
			// A stage that repeats what its column already says is noise.
			if n.Action == FlowActionAgent || n.Action == FlowActionDeploy || n.Action == FlowActionTest {
				t.Errorf("route %q stage %q repeats the column's action %q", f.Name, n.Column, n.Action)
			}
		}
	}
}
