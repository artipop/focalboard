package acp

import "strings"

// The routes an install starts with. They exist so a fresh board is not a blank
// registry: the "My Project Tasks" board template ships the columns these
// routes name and a "Workflow" property whose options are their names, so the
// whole mechanism can be seen — and picked — before anything is configured.
//
// Nothing is imposed by seeding them. A card takes a route only when it names
// one (its "Workflow" option), so a board that ignores the property behaves
// exactly as it did before: the standalone trigger columns and nothing else.
// Several routes also mean the "single registered flow" fallback in resolveFlow
// stays out of the way.
//
// Columns come from the config wherever the config already names one, so
// renaming triggerColumn renames the stage with it; the two stages with no
// config key of their own are named here.
const (
	TemplateReviewColumn  = "In Review"
	TemplateBlockedColumn = "Blocked"
	TemplateDoneColumn    = "Completed 🙌"
)

// Flow names, which are also the options of the board template's "Workflow"
// property — the two lists are kept in step by TestTemplateFlowsMatchTheBoardTemplate.
const (
	TemplateFlowFeature = "Feature"
	TemplateFlowHotfix  = "Hotfix"
	TemplateFlowReview  = "Review only"
)

// flowBuilder assembles one route, quietly dropping stages whose column the
// config leaves empty and any transition that would then dangle.
type flowBuilder struct {
	flow FlowEntry
}

func newFlow(name, property string) *flowBuilder {
	return &flowBuilder{flow: FlowEntry{Name: name, Property: property}}
}

// node adds a stage and returns its id, or "" when there is no column to put it
// on. Ids are stable strings rather than generated ones: they are what the
// engine records in flow_state, so a reseeded config must not invalidate them.
func (b *flowBuilder) node(id, column, action string) string {
	if strings.TrimSpace(column) == "" {
		return ""
	}
	for _, n := range b.flow.Nodes {
		if strings.EqualFold(n.Column, column) {
			return n.ID // two config keys naming one column: keep the first
		}
	}
	b.flow.Nodes = append(b.flow.Nodes, FlowNode{ID: id, Column: column, Action: action})
	return id
}

func (b *flowBuilder) edge(from, to, on string) {
	if from == "" || to == "" || from == to {
		return
	}
	b.flow.Edges = append(b.flow.Edges, FlowEdge{From: from, To: to, On: on})
}

// TemplateFlows builds the routes seeded on first run, from the config's own
// column names.
func TemplateFlows(cfg Config) []FlowEntry {
	var out []FlowEntry
	for _, f := range []FlowEntry{featureFlow(cfg), hotfixFlow(cfg), reviewFlow(cfg)} {
		if len(f.Nodes) > 0 {
			out = append(out, f)
		}
	}
	return out
}

// featureFlow is the long way round: write the code, wait for the branch to be
// merged, publish it, check the preview. A failed check goes back to the agent
// rather than to a person — that loop is the point of the whole thing.
func featureFlow(cfg Config) FlowEntry {
	b := newFlow(TemplateFlowFeature, cfg.TriggerProperty)
	agent := b.node("agent", cfg.TriggerColumn, FlowActionAgent)
	review := b.node("review", TemplateReviewColumn, FlowActionNone)
	deploy := b.node("deploy", cfg.DeployColumn, FlowActionDeploy)
	test := b.node("test", cfg.TestColumn, FlowActionTest)
	tested := b.node("tested", cfg.TestPassColumn, FlowActionNone)
	failed := b.node("failed", cfg.TestFailColumn, FlowActionNone)
	blocked := b.node("blocked", TemplateBlockedColumn, FlowActionNone)

	b.edge(agent, review, TriggerSuccess)
	b.edge(agent, blocked, TriggerFailure)
	// Merged rather than opened: the local git watcher sees it without a token,
	// so the route works on any hosting and with no credentials at all.
	b.edge(review, deploy, TriggerBranchMerged)
	b.edge(deploy, test, TriggerSuccess)
	b.edge(deploy, failed, TriggerFailure)
	b.edge(test, tested, TriggerSuccess)
	b.edge(test, agent, TriggerFailure)
	b.edge(test, blocked, TriggerBlocked)
	return b.flow
}

// hotfixFlow is the short way: written and published, no review and no browser
// pass. What it buys is speed, and it says so by having nowhere to check.
func hotfixFlow(cfg Config) FlowEntry {
	b := newFlow(TemplateFlowHotfix, cfg.TriggerProperty)
	agent := b.node("agent", cfg.TriggerColumn, FlowActionAgent)
	deploy := b.node("deploy", cfg.DeployColumn, FlowActionDeploy)
	done := b.node("done", TemplateDoneColumn, FlowActionNone)
	failed := b.node("failed", cfg.TestFailColumn, FlowActionNone)
	blocked := b.node("blocked", TemplateBlockedColumn, FlowActionNone)

	b.edge(agent, deploy, TriggerSuccess)
	b.edge(agent, blocked, TriggerFailure)
	b.edge(deploy, done, TriggerSuccess)
	b.edge(deploy, failed, TriggerFailure)
	return b.flow
}

// reviewFlow is for work that is never deployed from here — a refactor, a
// document, a spike. It ends when the branch lands in the main one.
func reviewFlow(cfg Config) FlowEntry {
	b := newFlow(TemplateFlowReview, cfg.TriggerProperty)
	agent := b.node("agent", cfg.TriggerColumn, FlowActionAgent)
	review := b.node("review", TemplateReviewColumn, FlowActionNone)
	done := b.node("done", TemplateDoneColumn, FlowActionNone)
	blocked := b.node("blocked", TemplateBlockedColumn, FlowActionNone)

	b.edge(agent, review, TriggerSuccess)
	b.edge(agent, blocked, TriggerFailure)
	b.edge(review, done, TriggerBranchMerged)
	return b.flow
}

// FlowTemplates returns the seeded routes rebuilt from the current config, for
// an install whose registry predates them: the editor offers whichever of them
// is missing, so they can be added without being retyped.
func (m *Manager) FlowTemplates() []FlowEntry {
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return TemplateFlows(m.cfg)
}
