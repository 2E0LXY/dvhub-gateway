// Package dsp holds small numeric helpers (FFT, windows).
package dsp

import (
	"math"
	"math/bits"
	"math/cmplx"
)

// FFT computes an in-place radix-2 DFT; len(x) must be a power of two.
// inverse=true computes the unscaled inverse.
func FFT(x []complex128, inverse bool) {
	n := len(x)
	if n&(n-1) != 0 {
		panic("dsp: FFT length not a power of two")
	}
	shift := 64 - uint(bits.TrailingZeros(uint(n)))
	for i := 0; i < n; i++ {
		j := int(bits.Reverse64(uint64(i)) >> shift)
		if j > i {
			x[i], x[j] = x[j], x[i]
		}
	}
	sign := -1.0
	if inverse {
		sign = 1
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, sign*2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				a, b := x[start+k], x[start+k+size/2]*wk
				x[start+k], x[start+k+size/2] = a+b, a-b
				wk *= w
			}
		}
	}
}

// Hamming returns a symmetric Hamming window of length n.
func Hamming(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1))
	}
	return w
}
