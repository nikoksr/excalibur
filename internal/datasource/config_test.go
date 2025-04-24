package datasource_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"excalibur/internal/datasource"
)

func TestConfig_Valid(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		cfg         datasource.Config
		expectValid bool
		expectedKey string // Key expected in the problems map if invalid
	}{
		{
			name:        "Valid DSN",
			cfg:         datasource.Config{DSN: "postgres://user:pass@host:port/db"},
			expectValid: true,
		},
		{
			name:        "Empty DSN",
			cfg:         datasource.Config{DSN: ""},
			expectValid: false,
			expectedKey: "dsn",
		},
		{
			name:        "Whitespace DSN",
			cfg:         datasource.Config{DSN: "   "},
			expectValid: false,
			expectedKey: "dsn",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			problems := tc.cfg.Valid(context.Background())

			if tc.expectValid {
				assert.Empty(t, problems, "Expected no validation problems")
			} else {
				require.NotEmpty(t, problems, "Expected validation problems")
				assert.Contains(t, problems, tc.expectedKey, "Expected problem key '%s' not found", tc.expectedKey)
				assert.NotEmpty(t, problems[tc.expectedKey], "Expected non-empty problem message for key '%s'", tc.expectedKey)
			}
		})
	}
}
