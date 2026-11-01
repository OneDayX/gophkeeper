// Package luhn validates numbers with the Luhn algorithm, the checksum that
// bank card numbers carry in their last digit.
package luhn

// Valid reports whether number is a non-empty string of ASCII digits whose
// last digit is the right Luhn check digit for the rest.
func Valid(number string) bool {
	if number == "" {
		return false
	}

	sum := 0
	// Walking from the right, every second digit is doubled, starting with
	// the one next to the check digit.
	double := false
	for i := len(number) - 1; i >= 0; i-- {
		c := number[i]
		if c < '0' || c > '9' {
			return false
		}

		digit := int(c - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}

		sum += digit
		double = !double
	}

	return sum%10 == 0
}
