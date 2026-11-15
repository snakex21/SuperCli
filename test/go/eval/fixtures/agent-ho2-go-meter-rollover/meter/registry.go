package meter

// digits maps a normalised model name to its register width.
var digits = map[string]int{
	"EM5": 5,
	"EM6": 6,
	"EMX": 7,
}

// DefaultDigits is the register width assumed for unknown models.
const DefaultDigits = 5

// Digits returns the register width of model.
func Digits(model string) int {
	if n, ok := digits[normalizeModel(model)]; ok {
		return n
	}
	return DefaultDigits
}

// Modulus returns the value at which the register of model wraps to zero.
func Modulus(model string) int64 {
	m := int64(1)
	for i := 0; i < Digits(model); i++ {
		m *= 10
	}
	return m
}

// Known reports whether model is a known model.
func Known(model string) bool {
	_, ok := digits[normalizeModel(model)]
	return ok
}
