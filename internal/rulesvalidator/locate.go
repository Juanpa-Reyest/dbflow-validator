package rulesvalidator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrRulesetMissing is returned by Locate when the validation ruleset YAML is
// absent from the cloned repository.  The pipeline must abort immediately —
// silent skip is not permitted.
var ErrRulesetMissing = errors.New("rulesvalidator: validation ruleset not found")

// Paths holds the host filesystem paths resolved for a given cloneRoot.
type Paths struct {
	// RulesetPath is the absolute path to validation-rules.yaml inside cloneRoot.
	RulesetPath string
	// SQLInputPath is the absolute path to the SQLInput directory inside cloneRoot.
	SQLInputPath string
}

// rulesetRelPath is the path of the validation ruleset relative to cloneRoot.
const rulesetRelPath = "src/main/resources/Validator/RulesContracts/validation-rules.yaml"

// sqlInputRelPath is the path of the SQL scripts directory relative to cloneRoot.
const sqlInputRelPath = "src/main/resources/SQLInput"

// Locate resolves the host-side paths for the validator inputs inside cloneRoot.
//
// It returns ErrRulesetMissing (wrapped) if the ruleset YAML does not exist —
// this is always a hard error; the caller must not proceed silently.
//
// The SQLInput path is always <cloneRoot>/src/main/resources/SQLInput at v1;
// no other path is accepted.
func Locate(cloneRoot string) (Paths, error) {
	rulesetPath := filepath.Join(cloneRoot, filepath.FromSlash(rulesetRelPath))
	if _, err := os.Stat(rulesetPath); err != nil {
		if os.IsNotExist(err) {
			return Paths{}, fmt.Errorf("%w: expected at %s", ErrRulesetMissing, rulesetPath)
		}
		return Paths{}, fmt.Errorf("rulesvalidator: stat ruleset %s: %w", rulesetPath, err)
	}

	sqlInputPath := filepath.Join(cloneRoot, filepath.FromSlash(sqlInputRelPath))

	return Paths{
		RulesetPath:  rulesetPath,
		SQLInputPath: sqlInputPath,
	}, nil
}
