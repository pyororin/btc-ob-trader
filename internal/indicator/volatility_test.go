// Copyright (c) 2024 OBI-Scalp-Bot
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package indicator

import (
	"math"
	"testing"
)

const float64EqualityThreshold = 1e-9

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) <= float64EqualityThreshold
}

func TestVolatilityCalculator_Update(t *testing.T) {
	tests := []struct {
		name          string
		lambda        float64
		prices        []float64
		expectedEWMAs []float64 // Expected EWMA of returns
		expectedStdDevs []float64 // Expected EWM Standard Deviation of returns
	}{
		{
			name:   "Stable price",
			lambda: 0.1, // Alpha in EWMA formula
			prices: []float64{100, 100, 100, 100},
			// Returns: 0, 0, 0
			// EWMA: 0, 0*0.1 + 0*0.9=0, 0*0.1 + 0*0.9=0
			// EWMVar: 0, (1-0.1)*0 + 0.1*(0-0)^2=0, (1-0.1)*0 + 0.1*(0-0)^2=0
			expectedEWMAs: []float64{0, 0, 0, 0}, // First is 0 as it's initialization
			expectedStdDevs: []float64{0, 0, 0, 0}, // First is 0
		},
		{
			name:   "Increasing price",
			lambda: 0.5, // Using lambda = 0.5 for simpler manual calculation
			prices: []float64{100, 101, 102, 103},
			// Prices: 100  | 101             | 102                  | 103
			// Return: init | (101-100)/100=0.01| (102-101)/101~=0.0099| (103-102)/102~=0.0098
			// prevP:  100  | 100             | 101                  | 102
			// EWMA_R: 0    | 0.5*0.01+0.5*0=0.005 | 0.5*0.0099+0.5*0.005 ~= 0.00745 | 0.5*0.0098 + 0.5*0.00745 ~= 0.008625
			// EWMVar_R:0   | (1-0.5)*0 + 0.5*(0.01)^2 = 0.00005 | (1-0.5)*0.00005 + 0.5*(0.0099)^2 ~= 0.000025 + 0.000049 = 0.000074 | ...
			// EWMStd_R:0   | sqrt(0.00005) ~= 0.00707 | sqrt(0.000074) ~= 0.0086
			expectedEWMAs: []float64{0, 0.005, 0.0074504950, 0.0086262376}, // Approximation, actual values below
			expectedStdDevs: []float64{0, 0.0070710678, 0.0086020168, 0.0091109085},
		},
		{
            name:   "Fluctuating price",
            lambda: 0.2,
            prices: []float64{100, 102, 100, 102},
            // Prices: 100 | 102               | 100                   | 102
            // Return: -   | (102-100)/100=0.02  | (100-102)/102~=-0.0196 | (102-100)/100=0.02
            // EWMA_R: 0   | 0.2*0.02+0.8*0=0.004| 0.2*(-0.0196)+0.8*0.004~=-0.00072 | 0.2*0.02+0.8*(-0.00072) ~=0.003424
            // EWMVar_R:0  | (1-0.2)*0 + 0.2*(0.02)^2 = 0.00008 | (1-0.2)*0.00008 + 0.2*(-0.0196)^2 ~= 0.000064 + 0.0000768 = 0.0001408 | ...
            // EWMStd_R:0  | sqrt(0.00008)~=0.00894 | sqrt(0.0001408)~=0.01186
            expectedEWMAs: []float64{0, 0.004, -0.0007254902, 0.0034196078},
            expectedStdDevs: []float64{0, 0.0089442719, 0.0118659004, 0.0147206903},
        },
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vc := NewVolatilityCalculator(tt.lambda)
			for i, price := range tt.prices {
				// First call initializes prevPrice, returns 0, 0
				if i == 0 {
					ewma, stdDev := vc.Update(price)
					if !almostEqual(ewma, tt.expectedEWMAs[i]) || !almostEqual(stdDev, tt.expectedStdDevs[i]) {
						t.Errorf("Initial Update(): EWMA got %v, want %v; StdDev got %v, want %v",
							ewma, tt.expectedEWMAs[i], stdDev, tt.expectedStdDevs[i])
					}
					continue
				}

				// Subsequent calls
				ewma, stdDev := vc.Update(price)
				currentExpectedEWMA := tt.expectedEWMAs[i]
				currentExpectedStdDev := tt.expectedStdDevs[i]

				// Manual calculation for increasing price case to get more precision for test
				if tt.name == "Increasing price" && i==1 { // 101
					// ret = (101-100)/100 = 0.01
					// ewmaReturn = 0.5 * 0.01 + 0.5 * 0 = 0.005
					// ewmVarReturn = (1-0.5)*0 + 0.5 * (0.01*0.01) = 0.5 * 0.0001 = 0.00005
					// stdDev = sqrt(0.00005) = 0.0070710678118654755
					currentExpectedEWMA = 0.005
					currentExpectedStdDev = math.Sqrt(0.00005)
				}
				if tt.name == "Increasing price" && i==2 { // 102
					// prevPrice = 101, ewmaReturn_prev = 0.005, ewmVarReturn_prev = 0.00005
					// ret = (102-101)/101 = 0.009900990099
					// ewmaReturn = 0.5 * ret + 0.5 * 0.005 = 0.5 * 0.009900990099 + 0.0025 = 0.00495049505 + 0.0025 = 0.00745049505
					// ewmVarReturn = (1-0.5)*0.00005 + 0.5*(ret*ret) = 0.000025 + 0.5 * (0.009900990099*0.009900990099)
					// = 0.000025 + 0.5 * 0.0000980296049 = 0.000025 + 0.00004901480245 = 0.00007401480245
					// stdDev = sqrt(0.00007401480245) = 0.008602023161
					currentExpectedEWMA = (0.5 * ((102.0-101.0)/101.0)) + (0.5 * 0.005)
                    ret_i2 := (102.0-101.0)/101.0
					currentExpectedStdDev = math.Sqrt( (0.5 * 0.00005) + 0.5*ret_i2*ret_i2 )
				}
                if tt.name == "Increasing price" && i==3 { // 103
                    // prevPrice = 102, ewmaReturn_prev = 0.00745049505, ewmVarReturn_prev = 0.00007401480245
                    // ret = (103-102)/102 = 0.0098039215686
                    // ewmaReturn = 0.5 * ret + 0.5 * ewmaReturn_prev
                    //            = 0.5 * 0.0098039215686 + 0.5 * 0.00745049505
                    //            = 0.0049019607843 + 0.003725247525 = 0.0086272083093
					currentExpectedEWMA = (0.5 * ((103.0-102.0)/102.0)) + (0.5 * ((0.5 * ((102.0-101.0)/101.0)) + (0.5 * 0.005)))
                    ret_i3 := (103.0-102.0)/102.0
                    ret_i2 := (102.0-101.0)/101.0
                    ewmVar_prev := (0.5 * 0.00005) + 0.5*ret_i2*ret_i2
					currentExpectedStdDev = math.Sqrt( (0.5 * ewmVar_prev) + 0.5*ret_i3*ret_i3 )
                }


				if tt.name == "Fluctuating price" && i==1 { // P=102
                    currentExpectedEWMA = 0.2 * ((102.0-100.0)/100.0) + 0.8 * 0.0
                    ret_i1 := (102.0-100.0)/100.0
                    currentExpectedStdDev = math.Sqrt(0.8 * 0.0 + 0.2 * ret_i1 * ret_i1)
                }
                if tt.name == "Fluctuating price" && i==2 { // P=100
                    ewma_prev := 0.2 * ((102.0-100.0)/100.0)
                    ret_i1 := (102.0-100.0)/100.0
                    ewmVar_prev := 0.2 * ret_i1 * ret_i1

                    ret_i2 := (100.0-102.0)/102.0
                    currentExpectedEWMA = 0.2 * ret_i2 + 0.8 * ewma_prev
                    currentExpectedStdDev = math.Sqrt(0.8*ewmVar_prev + 0.2*ret_i2*ret_i2)
                }
                if tt.name == "Fluctuating price" && i==3 { // P=102
                    ret_i1 := (102.0-100.0)/100.0
                    ewma_p1 := 0.2 * ret_i1
                    ewmVar_p1 := 0.2 * ret_i1 * ret_i1

                    ret_i2 := (100.0-102.0)/102.0
                    ewma_p2 := 0.2 * ret_i2 + 0.8 * ewma_p1
                    ewmVar_p2 := 0.8*ewmVar_p1 + 0.2*ret_i2*ret_i2

                    ret_i3 := (102.0-100.0)/100.0
                    currentExpectedEWMA = 0.2 * ret_i3 + 0.8 * ewma_p2
                    currentExpectedStdDev = math.Sqrt(0.8*ewmVar_p2 + 0.2*ret_i3*ret_i3)
                }


				if !almostEqual(ewma, currentExpectedEWMA) {
					t.Errorf("Update %d (price %v): EWMA got %v, want %v", i, price, ewma, currentExpectedEWMA)
				}
				if !almostEqual(stdDev, currentExpectedStdDev) {
					t.Errorf("Update %d (price %v): StdDev got %v, want %v", i, price, stdDev, currentExpectedStdDev)
				}
                // Also test getter methods
                if !almostEqual(vc.GetEWMAVolatility(), currentExpectedEWMA) {
                    t.Errorf("GetEWMAVolatility %d (price %v): got %v, want %v", i, price, vc.GetEWMAVolatility(), currentExpectedEWMA)
                }
                if !almostEqual(vc.GetEWMStandardDeviation(), currentExpectedStdDev) {
                    t.Errorf("GetEWMStandardDeviation %d (price %v): got %v, want %v", i, price, vc.GetEWMStandardDeviation(), currentExpectedStdDev)
                }
			}
		})
	}
}
