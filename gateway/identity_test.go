package main

import (
	"testing"
)

func TestParseCapabilities(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		expected []string
	}{
		{
			name:     "empty string",
			header:   "",
			expected: []string{},
		},
		{
			name:     "single capability",
			header:   "read",
			expected: []string{"read"},
		},
		{
			name:     "multiple capabilities",
			header:   "read,write,admin",
			expected: []string{"read", "write", "admin"},
		},
		{
			name:     "capabilities with spaces",
			header:   " read , write , admin ",
			expected: []string{"read", "write", "admin"},
		},
		{
			name:     "trailing comma",
			header:   "read,write,",
			expected: []string{"read", "write"},
		},
		{
			name:     "extra commas and spaces",
			header:   " read , , write , ,admin,",
			expected: []string{"read", "write", "admin"},
		},
		{
			name:     "only commas and spaces",
			header:   " , , , ",
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseCapabilities(tt.header)
			if len(result) != len(tt.expected) {
				t.Errorf("parseCapabilities(%q) returned %d capabilities, want %d", tt.header, len(result), len(tt.expected))
				return
			}
			for i, cap := range result {
				if cap != tt.expected[i] {
					t.Errorf("parseCapabilities(%q)[%d] = %q, want %q", tt.header, i, cap, tt.expected[i])
				}
			}
		})
	}
}

func TestCallerIdentityHasCapability(t *testing.T) {
	tests := []struct {
		name         string
		identity     CallerIdentity
		capability   string
		expected     bool
	}{
		{
			name: "has capability",
			identity: CallerIdentity{
				Capabilities: []string{"read", "write", "admin"},
			},
			capability: "write",
			expected:   true,
		},
		{
			name: "missing capability",
			identity: CallerIdentity{
				Capabilities: []string{"read", "write"},
			},
			capability: "admin",
			expected:   false,
		},
		{
			name: "empty capabilities",
			identity: CallerIdentity{
				Capabilities: []string{},
			},
			capability: "read",
			expected:   false,
		},
		{
			name: "nil capabilities",
			identity: CallerIdentity{
				Capabilities: nil,
			},
			capability: "read",
			expected:   false,
		},
		{
			name: "check empty capability name",
			identity: CallerIdentity{
				Capabilities: []string{"read", "write"},
			},
			capability: "",
			expected:   false,
		},
		{
			name: "exact match required",
			identity: CallerIdentity{
				Capabilities: []string{"readonly"},
			},
			capability: "read",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.identity.HasCapability(tt.capability)
			if result != tt.expected {
				t.Errorf("HasCapability(%q) = %v, want %v", tt.capability, result, tt.expected)
			}
		})
	}
}
