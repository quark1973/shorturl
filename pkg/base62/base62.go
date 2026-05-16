package base62

const alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// Encode converts an unsigned integer to a base62 string.
func Encode(num uint64) string {
	if num == 0 {
		return "0"
	}

	buf := make([]byte, 0, 11)
	for num > 0 {
		buf = append(buf, alphabet[num%62])
		num /= 62
	}

	for left, right := 0, len(buf)-1; left < right; left, right = left+1, right-1 {
		buf[left], buf[right] = buf[right], buf[left]
	}

	return string(buf)
}
