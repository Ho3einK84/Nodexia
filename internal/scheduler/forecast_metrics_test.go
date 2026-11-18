package scheduler

import (
	"testing"

	"github.com/Ho3einK84/Nodexia/internal/module/alerts"
	"github.com/Ho3einK84/Nodexia/internal/module/analytics"
)

func TestForecastAlertValues(t *testing.T) {
	tests := map[string]struct {
		out           analytics.ForecastOutput
		wantAvailable bool
		wantProjected bool
		wantDays      float64
	}{
		"no limit": {
			out:           analytics.ForecastOutput{Exhaustion: analytics.ExhaustionForecast{HasLimit: false}},
			wantAvailable: false,
		},
		"already over": {
			out: analytics.ForecastOutput{
				Risks:      analytics.ForecastRisks{Exhaustion: true},
				Exhaustion: analytics.ExhaustionForecast{HasLimit: true, AlreadyOver: true},
			},
			wantAvailable: true,
			wantProjected: true,
			wantDays:      0,
		},
		"will exhaust in 4 days": {
			out: analytics.ForecastOutput{
				Risks:      analytics.ForecastRisks{Exhaustion: true},
				Exhaustion: analytics.ExhaustionForecast{HasLimit: true, WillExhaust: true, DaysRemaining: 4},
			},
			wantAvailable: true,
			wantProjected: true,
			wantDays:      4,
		},
		"limit set but safe": {
			out: analytics.ForecastOutput{
				Risks:      analytics.ForecastRisks{Exhaustion: false},
				Exhaustion: analytics.ExhaustionForecast{HasLimit: true},
			},
			wantAvailable: true,
			wantProjected: false,
			wantDays:      alerts.DaysToExhaustionSafe,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			available, projected, days := forecastAlertValues(tc.out)
			if available != tc.wantAvailable {
				t.Fatalf("available = %v, want %v", available, tc.wantAvailable)
			}
			if !available {
				return
			}
			if projected != tc.wantProjected {
				t.Fatalf("projected = %v, want %v", projected, tc.wantProjected)
			}
			if days != tc.wantDays {
				t.Fatalf("days = %v, want %v", days, tc.wantDays)
			}
		})
	}
}
