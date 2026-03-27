package helpers

import (
	"strconv"
	"time"
)

// EpochMillis serializes time.Time as Unix milliseconds (int64) in JSON.
// Clients send and receive epoch ms — e.g. 1743043200000.
type EpochMillis struct {
	time.Time
}

func (e EpochMillis) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(e.UnixMilli(), 10)), nil
}

func (e *EpochMillis) UnmarshalJSON(data []byte) error {
	ms, err := strconv.ParseInt(string(data), 10, 64)
	if err != nil {
		return err
	}
	e.Time = time.UnixMilli(ms).UTC()
	return nil
}

// FromTime wraps a time.Time into EpochMillis.
func FromTime(t time.Time) EpochMillis {
	return EpochMillis{t}
}

// FromTimePtr wraps a *time.Time into *EpochMillis.
func FromTimePtr(t *time.Time) *EpochMillis {
	if t == nil {
		return nil
	}
	e := EpochMillis{*t}
	return &e
}
