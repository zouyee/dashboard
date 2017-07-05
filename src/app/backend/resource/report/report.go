package report

// Range ...
type Range struct {
	// The boundaries of the time range.
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	// The maximum time between two slices within the boundaries.
	Step string `json:"step,omitempty"`
}

// Meta userinfo
type Meta struct {
	Name      string `json:"name,omitempty"`
	NameSpace string `json:"namespace,omitempty"`
	User      string `json:"username,omitempty"`
}

// FormList ...
type FormList struct {
	Meta            Meta    `json:"meta"`
	Items           []*Form `json:"items"`
	CreateTimestamp string  `json:"createtimestamp"`
}

// Form report sth
type Form struct {
	// Meta: belong which FormList
	Meta Meta
	// Kind: cluster、node、application、pod
	Name string `json:"name,omitempty"`
	Kind string `json:"kind,omitempty"`
	// Resource: cpu、disk、or sth metric、health
	Resource string `json:"resource,omitempty"`
	// Target: max、min、avg
	Target string `json:"target,omitempty"`
	// Range: start time，end time and step
	Range *Range `json:"range,omitempty"`
}

// Info ...
type Info struct {
	Name            string `json:"name,omitempty"`
	CreateTimestamp string `json:"createtimestamp"`
}
