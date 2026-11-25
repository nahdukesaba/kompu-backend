package model

// ApplicationCode is Kompu's row in sso-balai's public.applications. Role and
// membership status are always read for this code, never for another app.
const ApplicationCode = "kompu"

// Membership statuses and roles in public.app_memberships.
const (
	MemberApproved = "approved"
	MemberPending  = "pending"
	MemberRejected = "rejected"
	MemberDisabled = "disabled"

	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Member is a Central user together with their Kompu membership, read from
// the Supabase database shared with sso-balai.
type Member struct {
	ID           string `json:"id"` // public.app_users.id (Central user.id), never the JWT sub
	Email        string `json:"email"`
	FullName     string `json:"fullName"`
	Role         string `json:"role"`
	Status       string `json:"-"`
	SessionValid bool   `json:"-"` // the auth.sessions row exists and belongs to this user
}

// ApprovedApplication represents an active application in the SSO ecosystem
// that the authenticated user is approved to access.
type ApprovedApplication struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	LaunchURL string `json:"launchUrl"`
	LogoURL   string `json:"logoUrl"`
}
