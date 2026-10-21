// Package profile defines candidate data shared by the CLI and future interfaces.
// A valid profile may be incomplete; consumers must check their own requirements.
package profile

import (
	"time"

	"jobwatch/internal/config"
)

const SchemaVersion = 1

// Snapshot is the portable import/export format. Revision is a write precondition:
// zero creates a profile, while a positive value must match the stored revision.
// UpdatedAt describes an export; imports never use it as the new write timestamp.
type Snapshot struct {
	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
	Profile   Profile   `json:"profile"`
}

type Profile struct {
	SchemaVersion int        `json:"schemaVersion"`
	Identity      Identity   `json:"identity"`
	Background    Background `json:"background"`
	Search        Search     `json:"search"`
	Resume        Resume     `json:"resume"`
	Outreach      Outreach   `json:"outreach"`
	Automation    Automation `json:"automation"`
}

type Identity struct {
	Name     string `json:"name,omitempty"`
	Email    string `json:"email,omitempty"`
	Phone    string `json:"phone,omitempty"`
	Location string `json:"location,omitempty"`
	Links    []Link `json:"links,omitempty"`
}

type Link struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type Background struct {
	Skills     []Skill      `json:"skills,omitempty"`
	Experience []Employment `json:"experience,omitempty"`
	Education  []Education  `json:"education,omitempty"`
	Projects   []Project    `json:"projects,omitempty"`
	Evidence   []Evidence   `json:"evidence,omitempty"`
}

type Skill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Level       string   `json:"level"` // used | familiar | adjacent
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type Employment struct {
	ID          string   `json:"id"`
	Company     string   `json:"company"`
	Title       string   `json:"title"`
	Location    string   `json:"location,omitempty"`
	StartDate   string   `json:"startDate"`         // YYYY-MM
	EndDate     string   `json:"endDate,omitempty"` // empty means ongoing
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type Education struct {
	ID            string `json:"id"`
	Institution   string `json:"institution"`
	Qualification string `json:"qualification"`
	StartDate     string `json:"startDate"`
	EndDate       string `json:"endDate,omitempty"`
}

type Project struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	URL         string   `json:"url,omitempty"`
	Status      string   `json:"status"` // confirmed | needs_review | planned
	EvidenceIDs []string `json:"evidenceIds,omitempty"`
}

type Evidence struct {
	ID     string   `json:"id"`
	Text   string   `json:"text"`
	Source string   `json:"source"`
	Status string   `json:"status"` // candidate-confirmed, not independently verified
	Tags   []string `json:"tags,omitempty"`
}

type Search struct {
	Sources                    []config.Company `json:"sources,omitempty"`
	Filters                    config.Filters   `json:"filters"`
	Preferences                Preferences      `json:"preferences"`
	MaxRequiredExperienceYears *int             `json:"maxRequiredExperienceYears,omitempty"`
}

type Preferences struct {
	WorkModes         []string            `json:"workModes,omitempty"` // ordered preference
	WorkAuthorization []WorkAuthorization `json:"workAuthorization,omitempty"`
	WillingToRelocate *bool               `json:"willingToRelocate,omitempty"`
	Compensation      []Compensation      `json:"compensation,omitempty"`
	CompanyTypes      []string            `json:"companyTypes,omitempty"`
	Notes             string              `json:"notes,omitempty"`
}

type WorkAuthorization struct {
	Country             string `json:"country"` // two-letter uppercase code
	Authorized          *bool  `json:"authorized,omitempty"`
	RequiresSponsorship *bool  `json:"requiresSponsorship,omitempty"`
}

type Compensation struct {
	Market   string `json:"market"`
	Currency string `json:"currency"`
	Period   string `json:"period"`            // annual | monthly | hourly
	Basis    string `json:"basis"`             // fixed | total
	Minimum  *int64 `json:"minimum,omitempty"` // whole currency units; nil is unknown
	Target   *int64 `json:"target,omitempty"`
}

type Resume struct {
	Template string `json:"template,omitempty"`
	MaxPages *int   `json:"maxPages,omitempty"`
}

type Outreach struct {
	Signature    string `json:"signature,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	AutoDraft    *bool  `json:"autoDraft,omitempty"`
	MaxEmployees *int   `json:"maxEmployees,omitempty"`
}

type Automation struct {
	MaxPerCycle      *int   `json:"maxPerCycle,omitempty"`
	DailyBudget      *int   `json:"dailyBudget,omitempty"`
	MinFitToNotify   *int   `json:"minFitToNotify,omitempty"`
	ManualBypassFit  *bool  `json:"manualBypassFit,omitempty"`
	UnknownFitPolicy string `json:"unknownFitPolicy,omitempty"` // notify | review | skip
}
