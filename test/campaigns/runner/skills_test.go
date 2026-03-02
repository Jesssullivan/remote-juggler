package main

import (
	"testing"
)

func TestParseSkillName(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "standard header",
			input: "### Skill: file-analysis\nWhen to use: analyzing files",
			want:  "file-analysis",
		},
		{
			name:  "with extra whitespace",
			input: "### Skill:   test-writer  \nSteps:\n1. Read file",
			want:  "test-writer",
		},
		{
			name:  "no header",
			input: "Some random text without skill header",
			want:  "",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSkillName(tt.input)
			if got != tt.want {
				t.Errorf("parseSkillName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitSkillSections(t *testing.T) {
	text := "### Skill: A\nSteps: do A\n\n---\n\n### Skill: B\nSteps: do B"
	sections := splitSkillSections(text)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if sections[0] != "### Skill: A\nSteps: do A" {
		t.Errorf("section 0 = %q", sections[0])
	}
	if sections[1] != "### Skill: B\nSteps: do B" {
		t.Errorf("section 1 = %q", sections[1])
	}
}

func TestSplitSkillSectionsEmpty(t *testing.T) {
	sections := splitSkillSections("")
	if len(sections) != 0 {
		t.Errorf("expected 0 sections, got %d", len(sections))
	}
}

func TestUpsertSkillNewSkill(t *testing.T) {
	existing := ""
	newSkill := "### Skill: file-analysis\nWhen to use: analyzing files\nSteps:\n1. Read file"
	result := upsertSkill(existing, newSkill)
	if result != newSkill {
		t.Errorf("expected new skill to be stored as-is, got: %s", result)
	}
}

func TestUpsertSkillAppend(t *testing.T) {
	existing := "### Skill: file-analysis\nSteps: analyze files"
	newSkill := "### Skill: test-writer\nSteps: write tests"
	result := upsertSkill(existing, newSkill)

	sections := splitSkillSections(result)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d: %s", len(sections), result)
	}
	if parseSkillName(sections[0]) != "file-analysis" {
		t.Errorf("first section should be file-analysis, got: %s", sections[0])
	}
	if parseSkillName(sections[1]) != "test-writer" {
		t.Errorf("second section should be test-writer, got: %s", sections[1])
	}
}

func TestUpsertSkillReplace(t *testing.T) {
	existing := "### Skill: file-analysis\nSteps: old steps\n\n---\n\n### Skill: test-writer\nSteps: write tests"
	newSkill := "### Skill: file-analysis\nSteps: improved steps with new insights"
	result := upsertSkill(existing, newSkill)

	sections := splitSkillSections(result)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections after replace, got %d: %s", len(sections), result)
	}
	if parseSkillName(sections[0]) != "file-analysis" {
		t.Errorf("first section should be file-analysis, got: %s", sections[0])
	}
	if sections[0] != newSkill {
		t.Errorf("first section should be replaced with new skill, got: %s", sections[0])
	}
	if parseSkillName(sections[1]) != "test-writer" {
		t.Errorf("second section should be test-writer, got: %s", sections[1])
	}
}

func TestUpsertSkillEmptyNew(t *testing.T) {
	existing := "### Skill: file-analysis\nSteps: analyze"
	result := upsertSkill(existing, "")
	if result != existing {
		t.Errorf("empty new skill should return existing unchanged, got: %s", result)
	}
}

func TestUpsertSkillNoHeader(t *testing.T) {
	existing := "### Skill: file-analysis\nSteps: analyze"
	newSkill := "Some freeform text without a skill header"
	result := upsertSkill(existing, newSkill)

	// Should append since we can't match by name.
	sections := splitSkillSections(result)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d: %s", len(sections), result)
	}
}
