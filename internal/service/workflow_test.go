package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/axmishell/kpccssl/internal/store"
)

func TestNormalizeSchedule(t *testing.T) {
	for _, raw := range []string{"daily 03:00", "weekly 1 03:00", "monthly 1 03:00"} {
		if _, err := normalizeSchedule(raw); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
	}
	for _, raw := range []string{"", "daily", "daily 25:00", "weekly 9 03:00", "cron * * * *"} {
		if _, err := normalizeSchedule(raw); err == nil {
			t.Fatalf("%q accepted", raw)
		}
	}
}

func TestScheduleMatches(t *testing.T) {
	// 2026-11-06 is a Friday (weekday 5).
	now := time.Date(2026, 11, 6, 3, 0, 0, 0, time.UTC)
	if !scheduleMatches("daily 03:00", now) {
		t.Fatal("daily did not match")
	}
	if scheduleMatches("daily 03:01", now) {
		t.Fatal("daily matched wrong minute")
	}
	if !scheduleMatches("weekly 5 03:00", now) {
		t.Fatal("weekly did not match")
	}
	if !scheduleMatches("monthly 6 03:00", now) {
		t.Fatal("monthly did not match")
	}
	if scheduleMatches("monthly 7 03:00", now) {
		t.Fatal("monthly matched wrong day")
	}
}

func TestEvaluateGraphNode(t *testing.T) {
	steps := map[string]*store.WorkflowStepRun{
		"ok":   {StepID: "ok", Status: "succeeded"},
		"fail": {StepID: "fail", Status: "failed"},
		"run":  {StepID: "run", Status: "running"},
	}
	if got := evaluateGraphNode(nil, steps); got != graphNodeReady {
		t.Fatalf("start node = %v", got)
	}
	if got := evaluateGraphNode([]WorkflowEdge{{From: "ok", To: "x", Condition: "success"}}, steps); got != graphNodeReady {
		t.Fatalf("success edge = %v", got)
	}
	if got := evaluateGraphNode([]WorkflowEdge{{From: "fail", To: "x", Condition: "success"}}, steps); got != graphSkip {
		t.Fatalf("failed success edge = %v", got)
	}
	if got := evaluateGraphNode([]WorkflowEdge{{From: "fail", To: "x", Condition: "failure"}}, steps); got != graphNodeReady {
		t.Fatalf("failure edge = %v", got)
	}
	if got := evaluateGraphNode([]WorkflowEdge{{From: "run", To: "x", Condition: "always"}}, steps); got != graphWait {
		t.Fatalf("running edge = %v", got)
	}
}

func TestValidateWorkflowGraph(t *testing.T) {
	def := WorkflowDefinition{Version: 2,
		Nodes: []WorkflowNode{
			{ID: "a", Type: StepWait, Config: json.RawMessage(`{"seconds":1}`)},
			{ID: "b", Type: StepNotify, Config: json.RawMessage(`{"title":"done"}`)},
		},
		Edges: []WorkflowEdge{{ID: "e1", From: "a", To: "b", Condition: "success"}},
	}
	if err := validateWorkflowDefinition(&def); err != nil {
		t.Fatal(err)
	}
	cycle := WorkflowDefinition{Version: 2,
		Nodes: []WorkflowNode{
			{ID: "a", Type: StepWait, Config: json.RawMessage(`{"seconds":1}`)},
			{ID: "b", Type: StepWait, Config: json.RawMessage(`{"seconds":1}`)},
		},
		Edges: []WorkflowEdge{
			{ID: "e1", From: "a", To: "b"},
			{ID: "e2", From: "b", To: "a"},
		},
	}
	if err := validateWorkflowDefinition(&cycle); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestGroupSteps(t *testing.T) {
	steps := []WorkflowStep{
		{ID: "a", Type: StepApply},
		{ID: "b", Type: StepDeploy, ParallelGroup: "g1"},
		{ID: "c", Type: StepDeploy, ParallelGroup: "g1"},
		{ID: "d", Type: StepNotify},
	}
	groups := groupSteps(steps)
	if len(groups) != 3 || len(groups[1]) != 2 {
		t.Fatalf("groups = %#v", groups)
	}
}

func TestValidateWorkflowDefinition(t *testing.T) {
	def := WorkflowDefinition{Version: 1, Steps: []WorkflowStep{
		{ID: "apply", Type: StepApply, Config: json.RawMessage(`{"certificate_id":"c1"}`)},
		{ID: "deploy", Type: StepDeploy},
		{ID: "notify", Type: StepNotify, Config: json.RawMessage(`{"title":"done"}`)},
	}}
	if err := validateWorkflowDefinition(&def); err != nil {
		t.Fatal(err)
	}
	bad := WorkflowDefinition{Version: 1, Steps: []WorkflowStep{{ID: "x", Type: "unknown"}}}
	if err := validateWorkflowDefinition(&bad); err == nil {
		t.Fatal("unknown step type accepted")
	}
	dup := WorkflowDefinition{Version: 1, Steps: []WorkflowStep{
		{ID: "x", Type: StepWait, Config: json.RawMessage(`{"seconds":1}`)},
		{ID: "x", Type: StepWait, Config: json.RawMessage(`{"seconds":1}`)},
	}}
	if err := validateWorkflowDefinition(&dup); err == nil {
		t.Fatal("duplicate step id accepted")
	}
}
