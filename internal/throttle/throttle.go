// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package throttle slows down guessing at a secret: a password, a code or a token. The
// website and the API share one, so that moving from one to the other doesn't start
// the count again.
package throttle

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// Failures before the wait starts. An address can be a whole office, so it gets
	// more of them than one address trying one account.
	freeForAddress = 20
	freeForPair    = 3

	longestWait = 15 * time.Minute
	// A stale entry is one that's been quiet this long.
	entryLife = 2 * longestWait
	// sweepAt is how many entries it takes to bother sweeping, so that a flood of
	// addresses can't grow the map for ever.
	sweepAt = 10000

	// An account gets a burst of failures from anywhere, then one a minute.
	accountBurst = 20
)

// strike is a run of failures.
type strike struct {
	failures int
	until    time.Time
	last     time.Time
}

// Throttle slows down guessing. It backs off by address, and by address and account
// together, which is what stops someone guessing at one account. Neither touches
// anyone else, so a stranger can't lock a person out.
//
// That leaves guessing from many addresses, which is what the limit per account is
// for. It only ever delays, by about a minute at worst, and an address the person has
// logged in from before skips it. State is in memory, so it starts again with the
// server, and a second replica has its own.
type Throttle struct {
	mu       sync.Mutex
	strikes  map[string]*strike
	accounts map[string]*rate.Limiter
}

func New() *Throttle {
	return &Throttle{strikes: map[string]*strike{}, accounts: map[string]*rate.Limiter{}}
}

func addressKey(ip string) string    { return "a|" + ip }
func pairKey(ip, name string) string { return "p|" + ip + "|" + name }
func (t *Throttle) lock() (unlock func()) {
	t.mu.Lock()
	return t.mu.Unlock
}

func (t *Throttle) remaining(key string, now time.Time) time.Duration {
	if s, ok := t.strikes[key]; ok && s.until.After(now) {
		return s.until.Sub(now)
	}
	return 0
}

// Wait returns how long this address has to wait before trying this account.
func (t *Throttle) Wait(ip, name string, now time.Time) time.Duration {
	defer t.lock()()
	return max(t.remaining(addressKey(ip), now), t.remaining(pairKey(ip, name), now))
}

func (t *Throttle) account(name string) *rate.Limiter {
	limiter, ok := t.accounts[name]
	if !ok {
		limiter = rate.NewLimiter(rate.Every(time.Minute), accountBurst)
		t.accounts[name] = limiter
	}
	return limiter
}

// AccountWait returns how long a stranger has to wait to try this account.
func (t *Throttle) AccountWait(name string, now time.Time) time.Duration {
	defer t.lock()()
	if t.account(name).TokensAt(now) >= 1 {
		return 0
	}
	return time.Minute
}

func (t *Throttle) strike(key string, free int, now time.Time) {
	s, ok := t.strikes[key]
	if !ok || now.Sub(s.last) > entryLife {
		s = &strike{}
		t.strikes[key] = s
	}
	s.failures++
	s.last = now
	if over := s.failures - free; over > 0 {
		wait := longestWait
		if over < 11 { // 1<<10 seconds is already past longestWait.
			wait = min(time.Second<<over, longestWait)
		}
		s.until = now.Add(wait)
	}
}

// Failed records a wrong guess.
func (t *Throttle) Failed(ip, name string, now time.Time) {
	defer t.lock()()
	t.strike(addressKey(ip), freeForAddress, now)
	t.strike(pairKey(ip, name), freeForPair, now)
	t.account(name).AllowN(now, 1)
	if len(t.strikes) > sweepAt {
		t.sweep(now)
	}
}

// Succeeded forgets this pair. The address keeps its strikes, because a guesser
// could otherwise clear them by logging in to an account of their own.
func (t *Throttle) Succeeded(ip, name string) {
	defer t.lock()()
	delete(t.strikes, pairKey(ip, name))
}

func (t *Throttle) sweep(now time.Time) {
	for key, s := range t.strikes {
		if now.Sub(s.last) > entryLife {
			delete(t.strikes, key)
		}
	}
	for name, limiter := range t.accounts {
		if limiter.TokensAt(now) >= accountBurst {
			delete(t.accounts, name)
		}
	}
}
