package legacydb

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
)

func TestUnsignedColumnsPreserveSQLiteScanConversions(test *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		test.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	test.Cleanup(func() { _ = database.Close() })
	values := []struct {
		name  string
		value any
	}{
		{"null", nil}, {"zero", int64(0)}, {"positive", int64(123456789)}, {"negative", int64(-1)},
		{"maximum signed", int64(math.MaxInt64)}, {"maximum uint32", int64(math.MaxUint32)}, {"overflow uint32", int64(math.MaxUint32) + 1},
		{"true", true}, {"false", false}, {"float integer", float64(42)}, {"float fraction", 42.5}, {"negative zero", math.Copysign(0, -1)},
		{"nan", math.NaN()}, {"infinity", math.Inf(1)}, {"empty", ""}, {"numeric text", "123456789"},
		{"unsigned maximum text", "18446744073709551615"}, {"unsigned overflow text", "18446744073709551616"},
		{"signed text", "-1"}, {"explicit plus text", "+1"}, {"padded text", " 1"}, {"decimal text", "1.0"},
		{"exponent text", "1e3"}, {"blob integer", []byte("123")}, {"blob null", []byte("null")}, {"empty blob", []byte{}},
	}
	for _, value := range values {
		test.Run(value.name, func(test *testing.T) {
			ctx := context.Background()
			original64, optimized64 := uint64(7), uint64Column(7)
			originalErr := database.QueryRowContext(ctx, "SELECT ?", value.value).Scan(&original64)
			optimizedErr := database.QueryRowContext(ctx, "SELECT ?", value.value).Scan(&optimized64)
			if (originalErr == nil) != (optimizedErr == nil) || original64 != uint64(optimized64) {
				test.Fatal("uint64 conversion semantics changed", original64, optimized64, originalErr, optimizedErr)
			}
			original32, optimized32 := uint32(7), uint32Column(7)
			originalErr = database.QueryRowContext(ctx, "SELECT ?", value.value).Scan(&original32)
			optimizedErr = database.QueryRowContext(ctx, "SELECT ?", value.value).Scan(&optimized32)
			if (originalErr == nil) != (optimizedErr == nil) || original32 != uint32(optimized32) {
				test.Fatal("uint32 conversion semantics changed", original32, optimized32, originalErr, optimizedErr)
			}
		})
	}
}

func TestUnsignedColumnsRejectNilDestinations(test *testing.T) {
	for _, destination := range []sql.Scanner{(*uint64Column)(nil), (*uint32Column)(nil)} {
		if err := destination.Scan(int64(1)); !errors.Is(err, ErrDatabase) {
			test.Fatal("nil numeric destination did not fail", err)
		}
	}
}
