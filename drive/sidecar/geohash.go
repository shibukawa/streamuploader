package sidecar

const geohashAlphabet = "0123456789bcdefghjkmnpqrstuvwxyz"

// Geohash encodes a coordinate with the given precision (characters).
func Geohash(lat, lon float64, precision int) string {
	if precision <= 0 {
		precision = 8
	}
	latRange := [2]float64{-90, 90}
	lonRange := [2]float64{-180, 180}
	out := make([]byte, 0, precision)
	bit, idx := 0, 0
	even := true
	for len(out) < precision {
		if even {
			mid := (lonRange[0] + lonRange[1]) / 2
			if lon >= mid {
				idx = idx*2 + 1
				lonRange[0] = mid
			} else {
				idx *= 2
				lonRange[1] = mid
			}
		} else {
			mid := (latRange[0] + latRange[1]) / 2
			if lat >= mid {
				idx = idx*2 + 1
				latRange[0] = mid
			} else {
				idx *= 2
				latRange[1] = mid
			}
		}
		even = !even
		bit++
		if bit == 5 {
			out = append(out, geohashAlphabet[idx])
			bit, idx = 0, 0
		}
	}
	return string(out)
}
