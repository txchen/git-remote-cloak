package main

import (
	"fmt"
	"os"

	"github.com/txchen/git-remote-cloak/internal/domain"
	"github.com/txchen/git-remote-cloak/internal/engine"
	"github.com/txchen/git-remote-cloak/internal/localstate"
	"github.com/txchen/git-remote-cloak/internal/secret"
	"golang.org/x/term"
)

func hasTransientSecret(explicitFile string) bool {
	_, environmentSet := os.LookupEnv("CLOAK_RECOVERY_SECRET")
	_, environmentFileSet := os.LookupEnv("CLOAK_RECOVERY_SECRET_FILE")
	return explicitFile != "" || environmentSet || environmentFileSet
}

// Explicit inputs override local storage and retain ambiguity validation.
// Clone and Rekey intentionally acquire their credentials separately.
func acquireRepositorySecret(explicitFile string, allowGeneration bool) (domain.RecoverySecret, error) {
	if !hasTransientSecret(explicitFile) {
		if directory, err := absoluteGitDirectory(); err == nil {
			if err := engine.NewWithLocalState(directory).ReconcileRecoverySecret(); err != nil {
				return domain.RecoverySecret{}, err
			}
			key, exists, err := localstate.LoadRecoverySecret(directory)
			if err != nil || exists {
				return key, err
			}
		}
	}
	return acquireSecret(explicitFile, allowGeneration)
}

func acquireCloneSecret(explicitFile string) (domain.RecoverySecret, error) {
	if hasTransientSecret(explicitFile) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return acquireSecret(explicitFile, false)
	}
	fmt.Fprint(os.Stderr, "Recovery Mnemonic (cloak-v1: and 24 words; input hidden): ")
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return domain.RecoverySecret{}, fmt.Errorf("read Recovery Mnemonic: %w", err)
	}
	return secret.Parse(string(value))
}
