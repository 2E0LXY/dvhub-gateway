package dsp

import (
	"math"
	"math/cmplx"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	n := 64
	x := make([]complex128, n)
	for i := range x {
		x[i] = complex(math.Sin(float64(i)*0.7)+0.3*float64(i%5), math.Cos(float64(i)))
	}
	y := append([]complex128(nil), x...)
	FFT(y, false)
	for k := 0; k < n; k++ {
		var s complex128
		for i := 0; i < n; i++ {
			s += x[i] * cmplx.Exp(complex(0, -2*math.Pi*float64(i*k)/float64(n)))
		}
		if cmplx.Abs(s-y[k]) > 1e-9 {
			t.Fatalf("bin %d", k)
		}
	}
	FFT(y, true)
	for i := range y {
		if cmplx.Abs(y[i]/complex(float64(n), 0)-x[i]) > 1e-9 {
			t.Fatal("inverse")
		}
	}
}
