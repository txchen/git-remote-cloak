// Package secret acquires and validates a Recovery Secret without persisting it.
package secret

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/txchen/git-remote-cloak/internal/domain"
	bip39 "github.com/tyler-smith/go-bip39"
)

const Prefix = "cloak-v1:"

// Sources names all configured non-interactive Recovery Secret sources.
type Sources struct {
	EnvironmentValue   string
	EnvironmentSet     bool
	EnvironmentFile    string
	EnvironmentFileSet bool
	ExplicitFile       string
}

// Acquire reads exactly one configured source and validates the mnemonic.
func Acquire(sources Sources, warning func(string)) (domain.RecoverySecret, error) {
	count := 0
	if sources.EnvironmentSet {
		count++
	}
	if sources.EnvironmentFileSet {
		count++
	}
	if sources.ExplicitFile != "" {
		count++
	}
	if count == 0 {
		return domain.RecoverySecret{}, errors.New("no Recovery Secret configured; set CLOAK_RECOVERY_SECRET, CLOAK_RECOVERY_SECRET_FILE, or --secret-file")
	}
	if count > 1 {
		return domain.RecoverySecret{}, errors.New("multiple Recovery Secret sources configured")
	}
	value := sources.EnvironmentValue
	if sources.EnvironmentFileSet {
		var err error
		value, err = readFile(sources.EnvironmentFile, warning)
		if err != nil {
			return domain.RecoverySecret{}, err
		}
	}
	if sources.ExplicitFile != "" {
		var err error
		value, err = readFile(sources.ExplicitFile, warning)
		if err != nil {
			return domain.RecoverySecret{}, err
		}
	}
	return Parse(value)
}

// Parse decodes the versioned 24-word BIP-39 entropy representation.
func Parse(value string) (domain.RecoverySecret, error) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, Prefix) {
		return domain.RecoverySecret{}, errors.New("invalid Recovery Mnemonic version")
	}
	mnemonic := strings.TrimSpace(strings.TrimPrefix(value, Prefix))
	if len(strings.Fields(mnemonic)) != 24 || !bip39.IsMnemonicValid(mnemonic) {
		return domain.RecoverySecret{}, errors.New("invalid Recovery Mnemonic")
	}
	entropy, err := bip39.EntropyFromMnemonic(mnemonic)
	if err != nil || len(entropy) != 32 {
		return domain.RecoverySecret{}, errors.New("invalid Recovery Mnemonic")
	}
	var result domain.RecoverySecret
	copy(result[:], entropy)
	return result, nil
}

// Mnemonic encodes 256 bits as the versioned 24-word representation.
func Mnemonic(entropy domain.RecoverySecret) (string, error) {
	mnemonic, err := bip39.NewMnemonic(entropy[:])
	if err != nil {
		return "", fmt.Errorf("encode Recovery Mnemonic: %w", err)
	}
	return Prefix + mnemonic, nil
}

// Generate creates a new operating-system-random Recovery Secret and mnemonic.
func Generate() (domain.RecoverySecret, string, error) {
	var recoverySecret domain.RecoverySecret
	if _, err := rand.Read(recoverySecret[:]); err != nil {
		return domain.RecoverySecret{}, "", fmt.Errorf("generate Recovery Secret: %w", err)
	}
	mnemonic, err := Mnemonic(recoverySecret)
	if err != nil {
		return domain.RecoverySecret{}, "", err
	}
	return recoverySecret, mnemonic, nil
}

func readFile(path string, warning func(string)) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("read Recovery Secret file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Recovery Secret path is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 && warning != nil {
		warning("warning: Recovery Secret file is readable by group or others")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read Recovery Secret file: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return "", errors.New("Recovery Secret file is empty")
	}
	return string(data), nil
}
