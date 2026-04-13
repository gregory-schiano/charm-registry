package api

import (
	"sort"
	"time"

	"github.com/gschiano/charm-registry/internal/core"
	"github.com/gschiano/charm-registry/internal/service"
)

type errorItemResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorListResponse struct {
	ErrorList []errorItemResponse `json:"error-list"`
}

type statusResponse struct {
	Status string `json:"status"`
}

type codeMessageResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type packageMetadataResponse struct {
	Authority       *string               `json:"authority"`
	Contact         *string               `json:"contact"`
	DefaultTrack    *string               `json:"default-track"`
	Description     *string               `json:"description"`
	ID              string                `json:"id"`
	Links           map[string][]string   `json:"links"`
	Media           []core.Media          `json:"media"`
	Name            string                `json:"name"`
	Private         bool                  `json:"private"`
	Publisher       core.Publisher        `json:"publisher"`
	Status          string                `json:"status"`
	Store           string                `json:"store"`
	Summary         *string               `json:"summary"`
	Title           *string               `json:"title"`
	TrackGuardrails []core.TrackGuardrail `json:"track-guardrails"`
	Tracks          []core.Track          `json:"tracks"`
	Type            string                `json:"type"`
	Website         *string               `json:"website"`
}

type registerPackageResponse struct {
	ID string `json:"id"`
}

type packageListResponse struct {
	Results []packageMetadataResponse `json:"results"`
}

type packageMetadataEnvelope struct {
	Metadata packageMetadataResponse `json:"metadata"`
}

type deletePackageResponse struct {
	PackageID string `json:"package-id"`
}

type revisionListItemResponse struct {
	Bases     []core.Base `json:"bases"`
	CreatedAt time.Time   `json:"created-at"`
	CreatedBy string      `json:"created-by"`
	Errors    []any       `json:"errors"`
	Revision  int         `json:"revision"`
	SHA384    string      `json:"sha3-384"`
	Size      int64       `json:"size"`
	Status    string      `json:"status"`
	Version   string      `json:"version"`
}

type revisionListResponse struct {
	Revisions []revisionListItemResponse `json:"revisions"`
}

type statusURLResponse struct {
	StatusURL string `json:"status-url"`
}

type uploadResultResponse struct {
	Successful bool    `json:"successful"`
	UploadID   *string `json:"upload_id"`
}

type resourceRevisionListItemResponse struct {
	Architectures   []string      `json:"architectures"`
	Bases           []core.Base   `json:"bases"`
	CreatedAt       time.Time     `json:"created-at"`
	Download        core.Download `json:"download"`
	Filename        string        `json:"filename"`
	PackageRevision *int          `json:"package-revision"`
	Revision        int           `json:"revision"`
}

type resourceRevisionListResponse struct {
	Revisions []resourceRevisionListItemResponse `json:"revisions"`
}

type resourceListResponse struct {
	Resources []service.ResourceListItemResponse `json:"resources"`
}

type releasedResponse struct {
	Released []core.Release `json:"released"`
}

type tracksCreatedResponse struct {
	NumTracksCreated int `json:"num-tracks-created"`
}

type resourceRevisionUpdatesResponse struct {
	NumResourceRevisionsUpdated int `json:"num-resource-revisions-updated"`
}

func newErrorListResponse(code, message string) errorListResponse {
	return errorListResponse{
		ErrorList: []errorItemResponse{{
			Code:    code,
			Message: message,
		}},
	}
}

func packageMetadata(pkg core.Package) packageMetadataResponse {
	tracks := make([]core.Track, len(pkg.Tracks))
	copy(tracks, pkg.Tracks)
	sort.Slice(tracks, func(i, j int) bool { return tracks[i].Name < tracks[j].Name })
	return packageMetadataResponse{
		Authority:       pkg.Authority,
		Contact:         pkg.Contact,
		DefaultTrack:    pkg.DefaultTrack,
		Description:     pkg.Description,
		ID:              pkg.ID,
		Links:           pkg.Links,
		Media:           pkg.Media,
		Name:            pkg.Name,
		Private:         pkg.Private,
		Publisher:       pkg.Publisher,
		Status:          pkg.Status,
		Store:           pkg.Store,
		Summary:         pkg.Summary,
		Title:           pkg.Title,
		TrackGuardrails: pkg.TrackGuardrails,
		Tracks:          tracks,
		Type:            pkg.Type,
		Website:         pkg.Website,
	}
}
