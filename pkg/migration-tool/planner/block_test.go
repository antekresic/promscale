package planner

import (
	"github.com/prometheus/prometheus/util/testutil"
	"testing"
	"time"
)

func TestCreateBlock(t *testing.T) {
	minute := time.Minute.Milliseconds()
	cases := []struct {
		name       string
		mint       int64
		maxt       int64
		fails      bool
		errMessage string
	}{
		{
			name: "mint_maxt_same",
			mint: 100 * minute,
			maxt: 100 * minute,
		},
		{
			name: "normal",
			mint: 100 * minute,
			maxt: 100*minute + 1,
		},
		{
			name:  "mint_less_than_maxt",
			mint:  100 * minute,
			maxt:  100*minute - 1,
			fails: true,
		},
		{
			name:  "mint_less_than_global_mint",
			mint:  100*minute - 1,
			maxt:  101 * minute,
			fails: true,
		},
		{
			name:  "maxt_greater_than_global_maxt",
			mint:  100 * minute,
			maxt:  110*minute + 1,
			fails: true,
		},
	}

	mint := 100 * minute
	maxt := 110 * minute
	plan := &Plan{
		Mint: mint,
		Maxt: maxt,
	}

	for _, c := range cases {
		_, err := plan.createBlock(c.mint, c.maxt)
		if c.fails {
			testutil.NotOk(t, err)
			continue
		}
		testutil.Ok(t, err)
	}
}