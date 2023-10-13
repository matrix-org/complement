package helpers

type RegistrationOpts struct {
	LocalpartSuffix string // default '' (don't care)
	DeviceID        string // default '' (generate new)
	Password        string // default 'complement_meets_min_password_requirement'
	IsAdmin         bool   // default false
}

type LoginOpts struct {
	Password string // default 'complement_meets_min_password_requirement'
}
