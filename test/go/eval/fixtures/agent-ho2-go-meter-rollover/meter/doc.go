// Package meter knows the electricity meter models in the field.
//
// Every model has a register with a fixed number of digits; when the
// register passes its maximum it wraps around to zero. Model names come
// from installer paperwork and are matched case-insensitively, ignoring
// punctuation and spaces: "em-6", "EM6" and "Em 6" are the same model.
// Unknown models are assumed to have 5 digits, the most common register.
package meter
