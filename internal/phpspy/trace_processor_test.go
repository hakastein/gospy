package phpspy

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

// tracesToFoldedStacksTest defines the structure for each test case.
type tracesToFoldedStacksTest struct {
	name               string
	trace              []string
	keepEntrypointName bool
	wantFoldedStack    string
	wantEntryPoint     string
	wantErr            error
}

// runTracesToFoldedStacksTests executes a slice of tracesToFoldedStacksTest cases.
func runTracesToFoldedStacksTests(t *testing.T, tests []tracesToFoldedStacksTest) {
	for _, tt := range tests {
		tt := tt // Capture range variable
		t.Run(tt.name, func(t *testing.T) {
			gotFoldedStack, gotEntryPoint, gotErr := tracesToFoldedStacks(tt.trace, tt.keepEntrypointName)
			if tt.wantErr != nil {
				assert.Error(t, gotErr, "Expected an error but got none")
				assert.EqualError(t, gotErr, tt.wantErr.Error(), "Error message should match")
			} else {
				assert.NoError(t, gotErr, "Did not expect an error but got one")
				assert.Equal(t, tt.wantFoldedStack, gotFoldedStack, "Folded stack should match the expected value")
				assert.Equal(t, tt.wantEntryPoint, gotEntryPoint, "Entry point should match the expected value")
			}
		})
	}
}

// TestTracesToFoldedStacks tests the tracesToFoldedStacks function across various scenarios.
func TestTracesToFoldedStacks(t *testing.T) {
	validInputsWithKeep := []tracesToFoldedStacksTest{
		{
			name: "With entrypoint name",
			trace: []string{
				"0 InitFunction <internal>:-1",
				"1 ServiceModule::HandleRequest /app/src/ServiceModule.php:45",
				"2 ServiceModule::Process /app/src/ServiceModule.php:30",
				"3 Utils::Helper /app/src/Utils.php:15",
			},
			keepEntrypointName: true,
			wantFoldedStack:    "Utils::Helper /app/src/Utils.php;ServiceModule::Process;ServiceModule::HandleRequest;InitFunction",
			wantEntryPoint:     "/app/src/Utils.php",
			wantErr:            nil,
		},
		{
			name: "Without entrypoint name",
			trace: []string{
				"0 InitFunction <internal>:-1",
				"1 ServiceModule::HandleRequest /app/src/ServiceModule.php:45",
				"2 ServiceModule::Process /app/src/ServiceModule.php:30",
				"3 Utils::Helper /app/src/Utils.php:15",
			},
			keepEntrypointName: false,
			wantFoldedStack:    "Utils::Helper;ServiceModule::Process;ServiceModule::HandleRequest;InitFunction",
			wantEntryPoint:     "/app/src/Utils.php",
			wantErr:            nil,
		},
	}

	invalidInputs := []tracesToFoldedStacksTest{
		{
			name: "Trace with Insufficient Length",
			trace: []string{
				"0 SingleFunction <internal>:-1",
			},
			keepEntrypointName: false,
			wantFoldedStack:    "",
			wantEntryPoint:     "",
			wantErr:            errors.New("trace insufficient length"),
		},
		{
			name: "Trace with Invalid Format - Missing Tokens",
			trace: []string{
				"0 InvalidTrace /app/src/Module.php",
				"1 MissingFields",
			},
			keepEntrypointName: false,
			wantFoldedStack:    "",
			wantEntryPoint:     "",
			wantErr:            errors.New("invalid trace format"),
		},
		{
			name: "Trace with Invalid Format - Missing Colon in File Info",
			trace: []string{
				"0 StartFunction <internal>:-1",
				"1 ServiceModule::Handle /app/src/ServiceModule.php",
			},
			keepEntrypointName: false,
			wantFoldedStack:    "",
			wantEntryPoint:     "",
			wantErr:            errors.New("invalid file info in trace"),
		},
		{
			name: "Trace with Missing Function Name",
			trace: []string{
				"0 <internal>:-1",
				"1 /app/src/ServiceModule.php:45",
			},
			keepEntrypointName: false,
			wantFoldedStack:    "",
			wantEntryPoint:     "",
			wantErr:            errors.New("invalid trace format"),
		},
	}

	t.Run("Valid Inputs", func(t *testing.T) {
		runTracesToFoldedStacksTests(t, validInputsWithKeep)
	})

	t.Run("Invalid Inputs", func(t *testing.T) {
		runTracesToFoldedStacksTests(t, invalidInputs)
	})
}
