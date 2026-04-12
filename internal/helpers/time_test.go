package helpers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEpochMillis_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		time time.Time
		want string
	}{
		{
			name: "known timestamp",
			time: time.Date(2025, 3, 27, 0, 0, 0, 0, time.UTC),
			want: "1743033600000",
		},
		{
			name: "unix epoch",
			time: time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
			want: "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := EpochMillis{tt.time}
			got, err := json.Marshal(e)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestEpochMillis_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMs  int64
		wantErr bool
	}{
		{
			name:   "valid epoch ms",
			input:  "1743033600000",
			wantMs: 1743033600000,
		},
		{
			name:   "zero",
			input:  "0",
			wantMs: 0,
		},
		{
			name:    "invalid string",
			input:   `"not-a-number"`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e EpochMillis
			err := json.Unmarshal([]byte(tt.input), &e)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantMs, e.UnixMilli())
			}
		})
	}
}

func TestFromTime(t *testing.T) {
	ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
	e := FromTime(ts)
	assert.Equal(t, ts, e.Time)
}

func TestFromTimePtr(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		got := FromTimePtr(nil)
		assert.Nil(t, got)
	})

	t.Run("non-nil input", func(t *testing.T) {
		ts := time.Date(2026, 4, 10, 12, 0, 0, 0, time.UTC)
		got := FromTimePtr(&ts)
		require.NotNil(t, got)
		assert.Equal(t, ts, got.Time)
	})
}

func TestEpochMillis_RoundTrip(t *testing.T) {
	original := time.Date(2026, 4, 10, 15, 30, 0, 0, time.UTC)
	e := FromTime(original)

	data, err := json.Marshal(e)
	require.NoError(t, err)

	var decoded EpochMillis
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.UnixMilli(), decoded.UnixMilli())
}
