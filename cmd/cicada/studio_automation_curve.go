package main

type studioCurvePoint struct {
	OldAt string  `json:"oldAt,omitempty"`
	At    string  `json:"at"`
	Value string  `json:"value"`
	Shape string  `json:"shape"`
	Curve float64 `json:"curve"`
}
