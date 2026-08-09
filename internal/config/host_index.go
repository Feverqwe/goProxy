package config

import (
	"math"
	"strings"
)

type ruleHostIndex struct {
	positiveExact  compactStringSet
	negativeExact  compactStringSet
	positiveSuffix compactStringSet
	negativeSuffix compactStringSet
}

type hostRuleBuilder struct {
	index    ruleHostIndex
	patterns []string
}

type hostTokenKind uint8

const (
	hostTokenFallback hostTokenKind = iota
	hostTokenPositiveExact
	hostTokenNegativeExact
	hostTokenPositiveSuffix
	hostTokenNegativeSuffix
)

type compactSetCapacity struct {
	count int
	bytes int
}

type hostRuleCapacity struct {
	positiveExact  compactSetCapacity
	negativeExact  compactSetCapacity
	positiveSuffix compactSetCapacity
	negativeSuffix compactSetCapacity
	fallback       int
}

func (b *hostRuleBuilder) addString(token string, forceNegation bool) {
	b.add([]byte(token), forceNegation)
}

func (b *hostRuleBuilder) add(token []byte, forceNegation bool) {
	if len(token) == 0 {
		return
	}

	kind, actual := classifyHostToken(token, forceNegation)
	switch kind {
	case hostTokenPositiveExact:
		if b.index.positiveExact.add(actual) {
			return
		}
	case hostTokenNegativeExact:
		if b.index.negativeExact.add(actual) {
			return
		}
	case hostTokenPositiveSuffix:
		if b.index.positiveSuffix.add(actual) {
			return
		}
	case hostTokenNegativeSuffix:
		if b.index.negativeSuffix.add(actual) {
			return
		}
	}

	b.addFallback(token, forceNegation)
	rawClean := token
	rawNegative := token[0] == '!'
	if rawNegative {
		rawClean = token[1:]
	}
	if len(rawClean) > 2 && rawClean[0] == '*' && rawClean[1] == '.' {
		expanded := rawClean[2:]
		if rawNegative {
			expanded = append([]byte{'!'}, expanded...)
		}
		b.addFallback(expanded, forceNegation)
	}
}

func classifyHostToken(token []byte, forceNegation bool) (hostTokenKind, []byte) {
	if len(token) == 0 {
		return hostTokenFallback, token
	}
	negative := forceNegation
	actual := token
	if !forceNegation && token[0] == '!' {
		negative = true
		actual = token[1:]
	}
	if len(actual) == 0 {
		return hostTokenFallback, actual
	}

	if len(actual) > 2 && actual[0] == '*' && actual[1] == '.' && !containsGlobMeta(actual[2:]) {
		if negative {
			return hostTokenNegativeSuffix, actual[2:]
		}
		return hostTokenPositiveSuffix, actual[2:]
	}
	if !containsGlobMeta(actual) {
		if negative {
			return hostTokenNegativeExact, actual
		}
		return hostTokenPositiveExact, actual
	}
	return hostTokenFallback, actual
}

func (b *hostRuleBuilder) addFallback(token []byte, forceNegation bool) {
	if forceNegation {
		b.patterns = append(b.patterns, "!"+string(token))
		return
	}
	b.patterns = append(b.patterns, string(token))
}

func containsGlobMeta(value []byte) bool {
	for _, b := range value {
		switch b {
		case '*', '?', '[', ']', '{', '}', '\\':
			return true
		}
	}
	return false
}

func (c *hostRuleCapacity) observe(token []byte, forceNegation bool) {
	kind, actual := classifyHostToken(token, forceNegation)
	if len(actual) > math.MaxUint16 {
		kind = hostTokenFallback
	}
	var target *compactSetCapacity
	switch kind {
	case hostTokenPositiveExact:
		target = &c.positiveExact
	case hostTokenNegativeExact:
		target = &c.negativeExact
	case hostTokenPositiveSuffix:
		target = &c.positiveSuffix
	case hostTokenNegativeSuffix:
		target = &c.negativeSuffix
	default:
		c.fallback++
		rawClean := token
		if len(rawClean) > 0 && rawClean[0] == '!' {
			rawClean = rawClean[1:]
		}
		if len(rawClean) > 2 && rawClean[0] == '*' && rawClean[1] == '.' {
			c.fallback++
		}
		return
	}
	target.count++
	target.bytes += len(actual)
}

func (b *hostRuleBuilder) reserve(capacity hostRuleCapacity) {
	b.index.positiveExact.reserve(capacity.positiveExact.count, capacity.positiveExact.bytes)
	b.index.negativeExact.reserve(capacity.negativeExact.count, capacity.negativeExact.bytes)
	b.index.positiveSuffix.reserve(capacity.positiveSuffix.count, capacity.positiveSuffix.bytes)
	b.index.negativeSuffix.reserve(capacity.negativeSuffix.count, capacity.negativeSuffix.bytes)
	if capacity.fallback > 0 {
		patterns := make([]string, len(b.patterns), len(b.patterns)+capacity.fallback)
		copy(patterns, b.patterns)
		b.patterns = patterns
	}
}

func (i *ruleHostIndex) match(host string) (positive, negative bool) {
	positive = i.positiveExact.has(host) || matchesDomainSuffix(&i.positiveSuffix, host)
	negative = i.negativeExact.has(host) || matchesDomainSuffix(&i.negativeSuffix, host)
	return positive, negative
}

func matchesDomainSuffix(set *compactStringSet, host string) bool {
	if set.has(host) {
		return true
	}
	for dot := strings.IndexByte(host, '.'); dot >= 0; {
		host = host[dot+1:]
		if set.has(host) {
			return true
		}
		dot = strings.IndexByte(host, '.')
	}
	return false
}
