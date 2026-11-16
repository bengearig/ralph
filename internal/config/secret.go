package config

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Kind says how a setting's value is interpreted.
type Kind int

const (
	// KindString is a plain value.
	KindString Kind = iota
	// KindSecret holds a reference to a secret, never the secret.
	KindSecret
	// KindList holds a list of text values, stored as a compact JSON array.
	KindList
)

const schemeEnv = "env"

var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// SecretRef points at a secret held outside the configuration.
type SecretRef struct {
	Scheme string
	Name   string
}

var errNotReference = errors.New("must be a reference of the form env:NAME")

// ParseSecretRef parses env:NAME. The error never contains s.
func ParseSecretRef(s string) (SecretRef, error) {
	name, ok := strings.CutPrefix(s, schemeEnv+":")
	if !ok || !envVarName.MatchString(name) {
		return SecretRef{}, errNotReference
	}
	return SecretRef{Scheme: schemeEnv, Name: name}, nil
}

// Secret resolves the reference held by the secret setting at path.
func (c *Config) Secret(path string) (string, error) {
	s, ok := c.registry.Lookup(path)
	if !ok || s.Kind != KindSecret {
		return "", fmt.Errorf("config: setting %q is not a secret setting", path)
	}
	v, ok := c.values[path]
	if !ok {
		return "", fmt.Errorf("config: secret setting %q is not set", path)
	}
	ref, err := ParseSecretRef(v.Value)
	if err != nil {
		return "", fmt.Errorf("config: setting %q %w", path, err)
	}
	val, ok := c.getenv(ref.Name)
	if !ok || val == "" {
		return "", fmt.Errorf("config: secret setting %q: environment variable %s is unset or empty", path, ref.Name)
	}
	return val, nil
}

// SecretResolves reports whether Secret would succeed.
func (c *Config) SecretResolves(path string) bool {
	_, err := c.Secret(path)
	return err == nil
}
