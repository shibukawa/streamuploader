package storage

import (
	"fmt"
	"strings"
	"time"
)

// Retention asks the object store to lock an object against deletion and
// overwrite. On S3-compatible stores it maps to Object Lock headers: Mode and
// RetainUntil to a retention period, LegalHold to a legal hold. The bucket
// must have Object Lock enabled or the store refuses the write, which is the
// intended fail-closed behaviour for a WORM deployment.
type Retention struct {
	// Mode is "governance" or "compliance"; empty means no retention period.
	Mode string
	// RetainUntil is the end of the retention period; required with Mode.
	RetainUntil time.Time
	// LegalHold keeps the object until the hold is lifted explicitly.
	LegalHold bool
}

// Active reports whether the retention still forbids deletion at now.
func (r *Retention) Active(now time.Time) bool {
	if r == nil {
		return false
	}
	if r.LegalHold {
		return true
	}
	return r.Mode != "" && now.Before(r.RetainUntil)
}

// Lock mode names accepted by LockPolicy.
const (
	LockModeNone       = ""
	LockModeGovernance = "governance"
	LockModeCompliance = "compliance"
	LockModeLegalHold  = "legal_hold"
)

// LockPolicy is the operator's choice of object lock for the locked key
// classes (originals, journal, audit). The zero value locks nothing.
type LockPolicy struct {
	// Mode is governance, compliance or legal_hold; empty disables locking.
	Mode string
	// Period is the retention period for governance and compliance; ignored
	// for legal_hold, which has no end.
	Period time.Duration
}

// ParseLockPolicy validates the mode and period as given by configuration.
func ParseLockPolicy(mode string, period time.Duration) (LockPolicy, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case LockModeNone, "none", "off":
		return LockPolicy{}, nil
	case LockModeGovernance, LockModeCompliance:
		if period <= 0 {
			return LockPolicy{}, fmt.Errorf("object lock mode %q needs a retention period", mode)
		}
		return LockPolicy{Mode: mode, Period: period}, nil
	case LockModeLegalHold:
		return LockPolicy{Mode: mode}, nil
	}
	return LockPolicy{}, fmt.Errorf("unknown object lock mode %q (governance, compliance, legal_hold)", mode)
}

// Enabled reports whether writes to locked classes carry a retention.
func (p LockPolicy) Enabled() bool { return p.Mode != LockModeNone }

// For returns the retention to attach to an object written at now, or nil
// when the policy locks nothing.
func (p LockPolicy) For(now time.Time) *Retention {
	switch p.Mode {
	case LockModeGovernance, LockModeCompliance:
		return &Retention{Mode: p.Mode, RetainUntil: now.UTC().Add(p.Period)}
	case LockModeLegalHold:
		return &Retention{LegalHold: true}
	}
	return nil
}

// String describes the policy for logs and the info endpoint.
func (p LockPolicy) String() string {
	switch p.Mode {
	case LockModeGovernance, LockModeCompliance:
		return p.Mode + " for " + p.Period.String()
	case LockModeLegalHold:
		return "legal hold (indefinite)"
	}
	return "none"
}
