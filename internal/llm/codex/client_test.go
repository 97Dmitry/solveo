package codex

import (
	"reflect"
	"testing"
)

func TestArgumentsWithModelAndReasoning(t *testing.T) {
	client := Client{Model: "gpt-6-astra", Reasoning: "high"}
	got, err := client.arguments([]string{"first.png", "second.png"}, "answer.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"exec", "--model", "gpt-6-astra", "-c", "model_reasoning_effort=high",
		"--ignore-user-config", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only",
		"--image", "first.png", "--image", "second.png",
		"--output-last-message", "answer.md", "-",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsRejectInvalidReasoning(t *testing.T) {
	_, err := (Client{Reasoning: "high\nmodel=other"}).arguments([]string{"screen.png"}, "answer.md")
	if err == nil {
		t.Fatal("expected invalid reasoning level to be rejected")
	}
}

func TestArgumentsAcceptNewProviderAdvertisedEfforts(t *testing.T) {
	for _, effort := range []string{"none", "minimal", "adaptive", "ultra"} {
		got, err := (Client{Reasoning: effort}).arguments(nil, "answer.md")
		if err != nil {
			t.Fatalf("%s: %v", effort, err)
		}
		found := false
		for _, arg := range got {
			if arg == "model_reasoning_effort="+effort {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing advertised effort %q", effort)
		}
	}
}
