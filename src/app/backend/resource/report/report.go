package report

// Range ...
type Range struct {
	// The boundaries of the time range.
	Start, End string
	// The maximum time between two slices within the boundaries.
	Step string
}

// Meta userinfo
type Meta struct {
	Name      string `json:"name"`
	NameSpace string `json:"namespace"`
	User      string `json:"username"`
}

// FormList
type FormList struct {
	Form Form `json:"form"`
}

// Form report sth
type Form struct {
	// Kind: cluster、node、application、pod
	Meta Meta   `json:"meta"`
	Kind string `json:"kind"`
	// Resource: cpu、disk、or sth metric、health
	Resource string `json:"resource"`
	// Target: max、min、avg
	Target string `json:"target"`
	// Range: start time，end time and step
	Range Range `json:"range"`
}
