// Package api exposes the administration and catalog HTTP API (/api/v1).
// Every endpoint except /api/v1/health requires the admin token.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/silirim/vodmax/internal/catalog"
	"github.com/silirim/vodmax/internal/service"
	"github.com/silirim/vodmax/internal/store"
	"github.com/silirim/vodmax/internal/web"
)

// Handler builds the router. token is the admin bearer token.
func Handler(svc *service.Service, token string, log *slog.Logger) http.Handler {
	a := &api{svc: svc, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	protected := http.NewServeMux()
	route := func(pattern string, level accessLevel, h http.HandlerFunc) {
		protected.Handle(pattern, a.require(level, h))
	}
	// Device level: before a profile is chosen.
	route("GET /api/v1/me", levelDevice, a.me)
	route("GET /api/v1/profiles", levelDevice, a.profiles)
	route("POST /api/v1/profiles/{id}/select", levelDevice, a.selectProfile)
	// Administration.
	route("GET /api/v1/avatars", levelDevice, a.avatars)
	route("PUT /api/v1/profile", levelViewer, a.putOwnProfile)
	route("POST /api/v1/profiles", levelAdmin, a.addProfile)
	route("PATCH /api/v1/profiles/{id}", levelAdmin, a.patchProfile)
	route("DELETE /api/v1/profiles/{id}", levelAdmin, a.deleteProfile)
	route("GET /api/v1/devices", levelAdmin, a.devices)
	route("PATCH /api/v1/devices/{id}", levelAdmin, a.patchDevice)
	route("DELETE /api/v1/devices/{id}", levelAdmin, a.deleteDevice)
	route("POST /api/v1/pair/approve", levelAdmin, a.approvePairing)
	route("GET /api/v1/vpn", levelAdmin, a.listVPN)
	route("POST /api/v1/vpn", levelAdmin, a.addVPN)
	route("POST /api/v1/vpn/{id}/connect", levelAdmin, a.connectVPN)
	route("POST /api/v1/vpn/{id}/disconnect", levelAdmin, a.disconnectVPN)
	route("DELETE /api/v1/vpn/{id}", levelAdmin, a.deleteVPN)
	route("PATCH /api/v1/vpn/{id}", levelAdmin, a.patchVPN)
	route("GET /api/v1/status", levelViewer, a.status)
	route("POST /api/v1/backup", levelAdmin, a.exportBackup)
	route("POST /api/v1/restore", levelAdmin, a.restoreBackup)
	route("GET /api/v1/backups", levelAdmin, a.listBackups)
	route("POST /api/v1/backups", levelAdmin, a.makeBackup)
	route("GET /api/v1/backups/{name}", levelAdmin, a.downloadBackup)
	route("POST /api/v1/backups/{name}/restore", levelAdmin, a.restoreAutoBackup)
	route("GET /api/v1/settings/schedule", levelAdmin, a.getSchedule)
	route("PUT /api/v1/settings/schedule", levelAdmin, a.putSchedule)
	route("PATCH /api/v1/subscriptions/{id}", levelAdmin, a.patchSubscription)
	route("GET /api/v1/folders", levelAdmin, a.listFolders)
	route("GET /api/v1/subscriptions", levelAdmin, a.listSubscriptions)
	route("POST /api/v1/subscriptions", levelAdmin, a.addSubscription)
	route("POST /api/v1/subscriptions/{id}/sync", levelAdmin, a.syncSubscription)
	route("DELETE /api/v1/subscriptions/{id}", levelAdmin, a.deleteSubscription)
	route("GET /api/v1/subscriptions/{id}/rules", levelAdmin, a.getRules)
	route("PUT /api/v1/subscriptions/{id}/rules", levelAdmin, a.putRules)
	route("POST /api/v1/subscriptions/{id}/rules/preview", levelAdmin, a.previewRules)
	route("GET /api/v1/subscriptions/{id}/selection", levelAdmin, a.selectionStats)
	route("GET /api/v1/subscriptions/{id}/categories", levelAdmin, a.selectionCategories)
	route("PUT /api/v1/subscriptions/{id}/categories/{kind}/{category}", levelAdmin, a.putCategory)
	route("PUT /api/v1/items/{id}/hidden", levelAdmin, a.putHidden)
	route("GET /api/v1/items/{id}/variants", levelViewer, a.variants)
	route("GET /api/v1/items/{id}/details", levelViewer, a.details)
	route("POST /api/v1/debug/player", levelViewer, a.playerLog)
	route("GET /api/v1/downloads", levelMember, a.listDownloads)
	route("POST /api/v1/downloads", levelMember, a.addDownload)
	route("POST /api/v1/downloads/{id}/pause", levelMember, a.pauseDownload)
	route("POST /api/v1/downloads/{id}/resume", levelMember, a.resumeDownload)
	route("DELETE /api/v1/downloads/{id}", levelMember, a.deleteDownload)
	route("GET /api/v1/settings/downloads", levelViewer, a.getDownloadSettings)
	route("PUT /api/v1/settings/downloads", levelAdmin, a.putDownloadSettings)
	route("GET /api/v1/update", levelDevice, a.getUpdate)
	route("POST /api/v1/update/check", levelAdmin, a.checkUpdate)
	route("PUT /api/v1/settings/updates", levelAdmin, a.putUpdateSettings)
	route("GET /api/v1/settings/metadata", levelAdmin, a.getMetadataSettings)
	route("PUT /api/v1/settings/metadata", levelAdmin, a.putMetadataSettings)
	route("GET /api/v1/catalog/{kind}", levelViewer, a.catalog)
	route("GET /api/v1/categories/{kind}", levelViewer, a.categories)
	route("GET /api/v1/browse/{kind}/categories", levelViewer, a.browseCategories)
	route("GET /api/v1/tv/channels", levelViewer, a.tvChannels)
	route("GET /api/v1/tv/channels/{id}/guide", levelViewer, a.channelGuide)
	route("GET /api/v1/guide", levelViewer, a.guide)
	route("GET /api/v1/guide/sources", levelAdmin, a.guideSources)
	route("POST /api/v1/guide/sources", levelAdmin, a.addGuideSource)
	route("PATCH /api/v1/guide/sources/{id}", levelAdmin, a.patchGuideSource)
	route("DELETE /api/v1/guide/sources/{id}", levelAdmin, a.deleteGuideSource)
	route("POST /api/v1/guide/sources/{id}/sync", levelAdmin, a.syncGuideSource)
	route("PUT /api/v1/tv/favorites/{id}", levelViewer, a.putFavorite)
	route("POST /api/v1/subscriptions/{id}/guide/sync", levelAdmin, a.syncGuide)
	route("POST /api/v1/live/{id}/ticket", levelViewer, a.liveTicket)
	route("POST /api/v1/live/{id}/timeshift", levelViewer, a.startTimeshift)
	route("POST /api/v1/catchup/{id}/session", levelViewer, a.catchupSession)
	route("GET /api/v1/settings/live", levelViewer, a.getLiveSettings)
	route("PUT /api/v1/settings/live", levelAdmin, a.putLiveSettings)
	route("GET /api/v1/series/{id}/episodes", levelViewer, a.episodes)
	route("PUT /api/v1/series/{id}/markers", levelViewer, a.seriesMarkers)
	route("GET /api/v1/settings/output", levelAdmin, a.getOutput)
	route("PUT /api/v1/settings/output", levelAdmin, a.putOutput)
	route("GET /api/v1/settings/transcode", levelAdmin, a.getTranscode)
	route("PUT /api/v1/settings/transcode", levelAdmin, a.putTranscode)
	route("POST /api/v1/settings/transcode/test", levelAdmin, a.testTranscode)
	route("GET /api/v1/settings/assistant", levelAdmin, a.assistantView)
	route("PUT /api/v1/settings/assistant", levelAdmin, a.putAssistant)
	route("POST /api/v1/settings/assistant/test", levelAdmin, a.testAssistant)
	route("POST /api/v1/settings/assistant/models", levelAdmin, a.assistantModels)
	route("POST /api/v1/settings/assistant/local", levelAdmin, a.installLocalAssistant)
	route("POST /api/v1/settings/assistant/local/cancel", levelAdmin, a.cancelLocalAssistant)
	route("DELETE /api/v1/settings/assistant/local", levelAdmin, a.removeLocalAssistant)
	route("GET /api/v1/assistant", levelViewer, a.assistantOn)
	route("POST /api/v1/assistant/rule", levelMember, a.assistantRule)
	route("POST /api/v1/assistant/tonight", levelViewer, a.assistantTonight)
	route("GET /api/v1/settings/sso", levelAdmin, a.ssoView)
	route("PUT /api/v1/settings/sso", levelAdmin, a.putSSO)
	route("GET /api/v1/mylist", levelViewer, a.myList)
	route("GET /api/v1/trending", levelViewer, a.trending)
	route("GET /api/v1/mylist/{id}", levelViewer, a.inMyList)
	route("PUT /api/v1/mylist/{id}", levelViewer, a.setMyList)
	route("PUT /api/v1/progress/{media}/{id}", levelViewer, a.putProgress)
	route("GET /api/v1/progress/{media}/{id}", levelViewer, a.getProgress)
	route("GET /api/v1/continue", levelViewer, a.continueWatching)
	route("POST /api/v1/vod/{media}/{id}/session", levelViewer, a.vodSession)
	route("GET /api/v1/recordings", levelViewer, a.recordings)
	route("POST /api/v1/recordings", levelMember, a.addRecording)
	route("DELETE /api/v1/recordings/{id}", levelMember, a.deleteRecording)
	route("POST /api/v1/recordings/{id}/ticket", levelViewer, a.recordingTicket)
	route("PUT /api/v1/recordings/{id}/protected", levelMember, a.protectRecording)
	route("GET /api/v1/recordings/space", levelViewer, a.recordingSpace)
	route("PUT /api/v1/settings/recordings", levelAdmin, a.putRecordingSettings)
	route("GET /api/v1/settings/commercials", levelAdmin, a.getCommercials)
	route("PUT /api/v1/settings/commercials", levelAdmin, a.putCommercials)
	route("GET /api/v1/recording-rules", levelMember, a.recordingRules)
	route("POST /api/v1/recording-rules", levelMember, a.addRecordingRule)
	route("POST /api/v1/recording-rules/preview", levelMember, a.previewRecordingRule)
	route("PUT /api/v1/recording-rules/{id}", levelMember, a.putRecordingRule)
	route("DELETE /api/v1/recording-rules/{id}", levelMember, a.deleteRecordingRule)
	mux.Handle("/api/", a.authenticate(token, protected))
	// Pairing a new device: no credentials yet.
	mux.HandleFunc("POST /api/v1/pair", a.startPairing)
	mux.HandleFunc("GET /api/v1/pair/{poll}", a.pairingResult)
	// Sign-in with an account of the house: also before pairing.
	mux.HandleFunc("GET /api/v1/sso", a.ssoButton)
	mux.HandleFunc("POST /api/v1/sso/claim", a.ssoClaim)
	mux.HandleFunc("GET /auth/sso/start", a.ssoStart)
	mux.HandleFunc("GET /auth/sso/callback", a.ssoCallback)
	// The stream is authorised by its single-use ticket: video players
	// cannot send an Authorization header.
	mux.HandleFunc("GET /stream/live/{ticket}", a.liveStream)
	// Timeshift buffers: the session id is the capability, like tickets.
	mux.HandleFunc("GET /stream/timeshift/{session}/{file}", a.timeshiftFile)
	mux.HandleFunc("POST /stream/timeshift/{session}/keepalive", a.timeshiftKeepAlive)
	mux.HandleFunc("POST /stream/timeshift/{session}/stop", a.timeshiftStop)
	mux.HandleFunc("GET /img/logo/{id}", a.logo)
	mux.HandleFunc("GET /img/episode/{id}", a.episodeThumb)
	// Output to other apps: the key in the address is the permission.
	mux.HandleFunc("GET /out/{key}/discover.json", a.outDiscover)
	mux.HandleFunc("GET /out/{key}/lineup_status.json", a.outLineupStatus)
	mux.HandleFunc("GET /out/{key}/lineup.json", a.outLineup)
	mux.HandleFunc("POST /out/{key}/lineup.post", a.outLineupStatus)
	mux.HandleFunc("GET /out/{key}/playlist.m3u", a.outPlaylist)
	mux.HandleFunc("GET /out/{key}/epg.xml", a.outGuide)
	mux.HandleFunc("GET /out/{key}/channel/{file}", a.outChannel)
	mux.HandleFunc("GET /img/meta/{id}/{which}", a.metaImage)
	mux.HandleFunc("GET /stream/vod/{session}", a.vodDirect)
	mux.HandleFunc("GET /stream/vod/{session}/remux", a.vodRemux)
	mux.HandleFunc("GET /stream/vod/{session}/subtitles", a.vodSubtitles)
	mux.HandleFunc("GET /stream/recording/{ticket}", a.recordingStream)
	mux.Handle("/", web.Handler())
	return withLang(mux)
}

// withLang reads the language of a request: X-VodMax-Lang (the choice
// made in the interface), else the browser's Accept-Language.
func withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := service.PickLang(r.Header.Get("X-VodMax-Lang"))
		if lang == "" {
			lang = service.PickLang(r.Header.Get("Accept-Language"))
		}
		if lang != "" {
			r = r.WithContext(service.WithLang(r.Context(), lang))
		}
		next.ServeHTTP(w, r)
	})
}

type api struct {
	svc *service.Service
	log *slog.Logger
}

type accessLevel int

const (
	levelDevice accessLevel = iota // a paired device
	levelViewer                    // and a chosen profile
	levelMember                    // not a kids profile
	levelAdmin                     // an admin profile (or the admin token)
)

type identityKey struct{}

func identity(r *http.Request) service.Identity {
	id, _ := r.Context().Value(identityKey{}).(service.Identity)
	return id
}

// authenticate resolves the device (Authorization: Bearer) and the profile
// (X-Profile, a token from /api/v1/profiles/{id}/select).
func (a *api) authenticate(master string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, err := a.svc.Authenticate(r.Context(), token, r.Header.Get("X-Profile"), master)
		if errors.Is(err, service.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": service.Localize(service.LangOf(r.Context()), err), "code": "unauthorized"})
			return
		}
		if err != nil {
			a.fail(w, r, err)
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, id)
		ctx = service.WithViewer(ctx, id.Viewer())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *api) require(level accessLevel, h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := identity(r)
		if level >= levelViewer && id.Profile == nil {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": service.Localize(service.LangOf(r.Context()), service.ErrProfileRequired), "code": "profile_required"})
			return
		}
		if (level == levelMember && id.Profile.Kind == "kids") || (level == levelAdmin && !id.Admin()) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": service.Localize(service.LangOf(r.Context()), service.ErrForbidden), "code": "forbidden"})
			return
		}
		h(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *api) fail(w http.ResponseWriter, r *http.Request, err error) {
	var bad service.ErrInvalid
	var busy service.ConnectionBusy
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &bad):
		status = http.StatusBadRequest
	case errors.Is(err, store.ErrNotFound):
		status = http.StatusNotFound
	case errors.As(err, &busy) && busy.Recording:
		// Held by recordings: nobody can take it over (409 offers to).
		status = http.StatusLocked
	case errors.Is(err, store.ErrInUse), errors.Is(err, service.ErrBusy), errors.Is(err, store.ErrSuspiciousDrop),
		errors.Is(err, service.ErrBusyRecording),
		errors.As(err, new(service.ConnectionBusy)):
		status = http.StatusConflict
	case errors.Is(err, service.ErrTicket), errors.Is(err, service.ErrBadSignature), errors.Is(err, service.ErrSession),
		errors.Is(err, service.ErrTimeshift):
		status = http.StatusForbidden
	case errors.Is(err, service.ErrNoTimeshift):
		status = http.StatusServiceUnavailable
	case errors.Is(err, service.ErrPIN), errors.Is(err, service.ErrCurrentPIN), errors.Is(err, service.ErrForbidden):
		status = http.StatusForbidden
	case errors.Is(err, service.ErrAdminPIN):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrPINLocked):
		status = http.StatusTooManyRequests
	case errors.Is(err, service.ErrPairing):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrAssistantOff):
		status = http.StatusConflict
	case errors.Is(err, service.ErrAssistant):
		status = http.StatusBadGateway
	case errors.Is(err, service.ErrNoImage), errors.Is(err, service.ErrNotReady):
		status = http.StatusNotFound
	case errors.As(err, new(*service.UpstreamError)):
		status = http.StatusBadGateway
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
	}
	if status == http.StatusInternalServerError {
		a.log.Error("richiesta fallita", "path", r.URL.Path, "err", err)
	}
	writeJSON(w, status, map[string]string{"error": service.Localize(service.LangOf(r.Context()), err)})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return service.Invalid("corpo della richiesta non valido: %s", err.Error())
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, service.Invalid("id non valido")
	}
	return id, nil
}

func (a *api) listVPN(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.VPNs(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) addVPN(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Config string `json:"config"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	id, err := a.svc.AddVPNKind(r.Context(), in.Name, in.Kind, in.Config)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (a *api) connectVPN(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	st, err := a.svc.ConnectVPN(ctx, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *api) disconnectVPN(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.svc.DisconnectVPN(id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) deleteVPN(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = a.svc.DeleteVPN(r.Context(), id)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Subscriptions(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// listFolders: the server's folders, to choose a local collection.
func (a *api) listFolders(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Folders(r.URL.Query().Get("path"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) addSubscription(w http.ResponseWriter, r *http.Request) {
	var in service.SubscriptionInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	id, err := a.svc.AddSubscription(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (a *api) syncSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	// The sync outlives a dropped client: the catalog must not be left
	// half-fetched because a browser tab closed.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	defer cancel()
	res, err := a.svc.Sync(ctx, id, r.URL.Query().Get("allow_drop") == "true")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *api) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = a.svc.DeleteSubscription(r.Context(), id)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) catalog(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	query := store.Query{
		Kind: r.PathValue("kind"), Text: q.Get("q"), CategoryID: q.Get("category"),
		Sort: q.Get("sort"), Section: q.Get("section"), All: q.Get("all") == "true" && identity(r).Admin(), Cursor: q.Get("cursor"),
	}
	query.Limit, _ = strconv.Atoi(q.Get("limit"))
	query.MinRating, _ = strconv.ParseFloat(q.Get("min_rating"), 64)
	query.SubscriptionID, _ = strconv.ParseInt(q.Get("subscription"), 10, 64)
	if len(query.Text) > 200 {
		query.Text = query.Text[:200]
	}
	page, err := a.svc.Catalog(r.Context(), query)
	if err != nil {
		if strings.Contains(err.Error(), "non valido") {
			err = service.ErrInvalid{Msg: err.Error()}
		}
		a.fail(w, r, err)
		return
	}
	if page.Items == nil {
		page.Items = []store.CatalogItem{}
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *api) categories(w http.ResponseWriter, r *http.Request) {
	sub, _ := strconv.ParseInt(r.URL.Query().Get("subscription"), 10, 64)
	list, err := a.svc.Categories(r.Context(), r.PathValue("kind"), sub)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if list == nil {
		list = []store.Category{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) getRules(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	rules, err := a.svc.Rules(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if rules.Countries == nil {
		rules.Countries = []string{}
	}
	writeJSON(w, http.StatusOK, rules)
}

func (a *api) putRules(w http.ResponseWriter, r *http.Request) {
	a.rules(w, r, a.svc.SetRules)
}

func (a *api) previewRules(w http.ResponseWriter, r *http.Request) {
	a.rules(w, r, a.svc.PreviewRules)
}

func (a *api) rules(w http.ResponseWriter, r *http.Request, apply func(context.Context, int64, catalog.Rules) (catalog.Stats, error)) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var rules catalog.Rules
	if err := decode(r, &rules); err != nil {
		a.fail(w, r, err)
		return
	}
	stats, err := apply(r.Context(), id, rules)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (a *api) selectionStats(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	stats, err := a.svc.SubscriptionStats(r.Context(), id, nil)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (a *api) selectionCategories(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	list, err := a.svc.SelectionCategories(r.Context(), id, r.URL.Query().Get("kind"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) putCategory(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var o store.CategoryOverride
	if err := decode(r, &o); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetCategoryOverride(r.Context(), id, r.PathValue("kind"), r.PathValue("category"), o); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) putHidden(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Hidden bool `json:"hidden"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetItemHidden(r.Context(), id, in.Hidden); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) variants(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	list, err := a.svc.Variants(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) tvChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := a.svc.TVChannels(r.Context(), q.Get("section"), q.Get("list") == "favorites")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) channelGuide(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	list, err := a.svc.ChannelGuide(r.Context(), id, hours)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) putFavorite(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Favorite bool `json:"favorite"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetFavorite(r.Context(), id, in.Favorite); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) syncGuide(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	res, err := a.svc.SyncProviderGuide(ctx, id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *api) liveTicket(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Viewer string `json:"viewer"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	t, err := a.svc.LiveTicket(r.Context(), id, in.Viewer)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.svc.StopViewerTimeshift(in.Viewer) // the direct stream replaces the buffer
	writeJSON(w, http.StatusOK, map[string]string{"url": "/stream/live/" + t})
}

func (a *api) liveStream(w http.ResponseWriter, r *http.Request) {
	stream, err := a.svc.OpenLive(r.Context(), r.PathValue("ticket"), r.URL.Query().Get("force") == "1")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	relay(w, stream, http.StatusOK)
}

// relay copies a stream to the client, flushing as data arrives.
func relay(w http.ResponseWriter, stream *service.Stream, status int) {
	defer stream.Close()
	w.Header().Set("Content-Type", stream.ContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64<<10)
	for {
		n, err := stream.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return // viewer gone
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func (a *api) logo(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	img, err := a.svc.Logo(r.Context(), id, r.URL.Query().Get("s"))
	a.image(w, r, img, err)
}

func (a *api) episodeThumb(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	img, err := a.svc.EpisodeThumb(r.Context(), id, r.URL.Query().Get("s"))
	a.image(w, r, img, err)
}

func (a *api) metaImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	img, err := a.svc.MetaImage(r.Context(), id, r.PathValue("which"), r.URL.Query().Get("s"))
	a.image(w, r, img, err)
}

func (a *api) image(w http.ResponseWriter, r *http.Request, img store.CachedImage, err error) {
	if err != nil {
		if errors.Is(err, service.ErrNoImage) {
			w.Header().Set("Cache-Control", "max-age=3600")
		}
		a.fail(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", img.ContentType)
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(img.Data)
}

func (a *api) episodes(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	list, err := a.svc.Episodes(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// ---- Output to other apps ----

// baseURL is the address the request came to, as other apps reach it.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Host
	// Behind a reverse proxy that changes the name (only used to build
	// addresses shown to the user or registered with a provider).
	if h := r.Header.Get("X-Forwarded-Host"); h != "" && !strings.ContainsAny(h, "/ ,") {
		host = h
	}
	return scheme + "://" + host
}

func (a *api) outputView(r *http.Request, o service.OutputSettings) map[string]any {
	out := map[string]any{"enabled": o.Enabled, "tuners": a.svc.OutputTuners(r.Context())}
	if o.Key != "" {
		out["links"] = service.OutputLinks(baseURL(r), o)
	}
	return out
}

func (a *api) getOutput(w http.ResponseWriter, r *http.Request) {
	o, err := a.svc.OutputSettings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.outputView(r, o))
}

func (a *api) putOutput(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
		NewKey  bool `json:"new_key"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	o, err := a.svc.SetOutput(r.Context(), in.Enabled, in.NewKey)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.outputView(r, o))
}

func (a *api) outFail(w http.ResponseWriter, err error) {
	if errors.Is(err, service.ErrOutputOff) || errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, nil)
		return
	}
	http.Error(w, "VodMax: "+err.Error(), http.StatusServiceUnavailable)
}

func (a *api) outDiscover(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.OutputDevice(r.Context(), r.PathValue("key"), baseURL(r))
	if err != nil {
		a.outFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *api) outLineupStatus(w http.ResponseWriter, r *http.Request) {
	if _, err := a.svc.OutputDevice(r.Context(), r.PathValue("key"), baseURL(r)); err != nil {
		a.outFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ScanInProgress": 0, "ScanPossible": 1, "Source": "Cable", "SourceList": []string{"Cable"}})
}

func (a *api) outLineup(w http.ResponseWriter, r *http.Request) {
	l, err := a.svc.OutputLineup(r.Context(), r.PathValue("key"), baseURL(r))
	if err != nil {
		a.outFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (a *api) outPlaylist(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if err := a.svc.WriteOutputPlaylist(r.Context(), &buf, r.PathValue("key"), baseURL(r)); err != nil {
		a.outFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	w.Write(buf.Bytes())
}

func (a *api) outGuide(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	if err := a.svc.WriteOutputGuide(r.Context(), &buf, r.PathValue("key"), baseURL(r)); err != nil {
		a.outFail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Write(buf.Bytes())
}

func (a *api) outChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(strings.TrimSuffix(r.PathValue("file"), ".ts"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stream, err := a.svc.OpenOutput(r.Context(), r.PathValue("key"), id)
	if err != nil {
		a.outFail(w, err)
		return
	}
	relay(w, stream, http.StatusOK)
}

func (a *api) trending(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Trending(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) myList(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.MyList(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) inMyList(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	on, err := a.svc.InMyList(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": on})
}

func (a *api) setMyList(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		On bool `json:"on"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetMyList(r.Context(), id, in.On); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"on": in.On})
}

func (a *api) seriesMarkers(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in store.SeriesMarkers
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetSeriesMarkers(r.Context(), id, in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) putProgress(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Position int `json:"position"`
		Duration int `json:"duration"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.SaveProgress(r.Context(), r.PathValue("media"), id, in.Position, in.Duration)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *api) continueWatching(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Continue(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) vodSession(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Viewer string `json:"viewer"`
		Mode   string `json:"mode"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	sess, err := a.svc.NewPlaybackSession(ctx, r.PathValue("media"), id, in.Viewer, in.Mode)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *api) vodDirect(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	stream, err := a.svc.OpenDirect(r.Context(), r.PathValue("session"), r.Header.Get("Range"), r.URL.Query().Get("force") == "1")
	if err != nil {
		a.log.Info("film diretto: richiesta rifiutata", "range", r.Header.Get("Range"), "err", err)
		a.fail(w, r, err)
		return
	}
	// What each browser request got, to diagnose playback.
	counted := &countingReader{ReadCloser: stream.ReadCloser}
	stream.ReadCloser = counted
	defer func() {
		a.log.Info("film diretto: richiesta", "range", r.Header.Get("Range"), "risposta", stream.ContentRange,
			"inviati_mb", float64(counted.n)/1e6, "durata_s", time.Since(started).Seconds(), "fine", counted.end())
	}()
	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	if stream.ContentLength >= 0 {
		h.Set("Content-Length", strconv.FormatInt(stream.ContentLength, 10))
	}
	if stream.ContentRange != "" {
		h.Set("Content-Range", stream.ContentRange)
	}
	relay(w, stream, stream.Status)
}

func (a *api) vodRemux(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	t, _ := strconv.ParseFloat(q.Get("t"), 64)
	audio, sub := -1, -1
	if v, err := strconv.Atoi(q.Get("audio")); err == nil {
		audio = v
	}
	if v, err := strconv.Atoi(q.Get("sub")); err == nil {
		sub = v
	}
	stream, err := a.svc.OpenRemux(r.Context(), r.PathValue("session"), max(t, 0), q.Get("force") == "1", audio, sub)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	relay(w, stream, http.StatusOK)
}

func (a *api) recordings(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Recordings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) addRecording(w http.ResponseWriter, r *http.Request) {
	var in service.RecordingRequest
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	rec, err := a.svc.ScheduleRecording(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

func (a *api) deleteRecording(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err == nil {
		err = a.svc.DeleteRecording(r.Context(), id)
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) recordingTicket(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	t, breaks, err := a.svc.RecordingTicket(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if breaks == nil {
		breaks = []service.Commercial{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": "/stream/recording/" + t, "commercials": breaks})
}

func (a *api) recordingStream(w http.ResponseWriter, r *http.Request) {
	file, stream, rec, err := a.svc.OpenRecording(r.Context(), r.PathValue("ticket"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if stream != nil {
		relay(w, stream, http.StatusOK)
		return
	}
	defer file.Close()
	ctype := "video/mp2t"
	if strings.HasSuffix(file.Name(), ".mp4") {
		ctype = "video/mp4"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", time.Unix(rec.Stop, 0), file)
}

func (a *api) getProgress(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	p, err := a.svc.Progress(r.Context(), r.PathValue("media"), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if p == nil {
		writeJSON(w, http.StatusOK, map[string]any{"position": 0, "duration": 0, "completed": false})
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *api) patchVPN(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Name   *string `json:"name"`
		Config *string `json:"config"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.UpdateVPN(r.Context(), id, in.Name, in.Config); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) patchSubscription(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in service.SubscriptionPatch
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.UpdateSubscription(r.Context(), id, in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) status(w http.ResponseWriter, r *http.Request) {
	st, err := a.svc.Status(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (a *api) getSchedule(w http.ResponseWriter, r *http.Request) {
	sc, err := a.svc.Schedule(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (a *api) putSchedule(w http.ResponseWriter, r *http.Request) {
	var sc service.Schedule
	if err := decode(r, &sc); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetSchedule(r.Context(), sc); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func backupHeaders(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
}

func (a *api) exportBackup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	data, err := a.svc.ExportBackup(r.Context(), in.Password)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	backupHeaders(w, time.Now().Format("vodmax-backup-20060102")+".vodmaxbackup")
	w.Write(data)
}

// restoreBackup takes the file as the request body and the password in a
// header (never in the URL, which may end up in logs).
func (a *api) restoreBackup(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
	if err != nil {
		a.fail(w, r, service.Invalid("file del backup troppo grande o interrotto"))
		return
	}
	if err := a.svc.RestoreBackup(r.Context(), data, r.Header.Get("X-Backup-Password")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) listBackups(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Backups()
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) makeBackup(w http.ResponseWriter, r *http.Request) {
	b, err := a.svc.AutoBackup(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, b)
}

func (a *api) downloadBackup(w http.ResponseWriter, r *http.Request) {
	data, err := a.svc.AutoBackupData(r.PathValue("name"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	backupHeaders(w, r.PathValue("name"))
	w.Write(data)
}

func (a *api) restoreAutoBackup(w http.ResponseWriter, r *http.Request) {
	data, err := a.svc.AutoBackupData(r.PathValue("name"))
	if err == nil {
		err = a.svc.RestoreBackup(r.Context(), data, "")
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Profiles, devices, pairing ----

func (a *api) me(w http.ResponseWriter, r *http.Request) {
	id := identity(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"device":    map[string]any{"id": id.DeviceID, "name": id.Device, "profile_lock": id.Lock},
		"profile":   id.Profile,
		"admin":     id.Admin(),
		"is_master": id.DeviceID == service.MasterDevice,
		// An admin profile that must set its PIN before anything else.
		"pin_required": id.NeedsPIN,
	})
}

func (a *api) avatars(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string][]string{"avatars": service.Avatars, "colors": service.AvatarColors})
}

func (a *api) putOwnProfile(w http.ResponseWriter, r *http.Request) {
	var in service.OwnProfileInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.UpdateOwnProfile(r.Context(), identity(r), in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) profiles(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.SelectableProfiles(r.Context(), identity(r))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) selectProfile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		PIN string `json:"pin"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	token, err := a.svc.SelectProfile(r.Context(), identity(r), id, in.PIN)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"profile_token": token})
}

func (a *api) addProfile(w http.ResponseWriter, r *http.Request) {
	var in service.ProfileInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	id, err := a.svc.AddProfile(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (a *api) patchProfile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in service.ProfileInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.UpdateProfile(r.Context(), id, in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.DeleteProfile(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) devices(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Devices(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) patchDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Name        string `json:"name"`
		ProfileLock *int64 `json:"profile_lock"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.UpdateDevice(r.Context(), id, in.Name, in.ProfileLock); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.DeleteDevice(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) approvePairing(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code        string `json:"code"`
		Name        string `json:"name"`
		ProfileLock *int64 `json:"profile_lock"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	d, err := a.svc.ApprovePairing(r.Context(), in.Code, in.Name, in.ProfileLock)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (a *api) startPairing(w http.ResponseWriter, r *http.Request) {
	client, _, _ := net.SplitHostPort(r.RemoteAddr)
	p, err := a.svc.StartPairing(client)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

// pairingResult: 202 while waiting, then the device token once.
func (a *api) pairingResult(w http.ResponseWriter, r *http.Request) {
	token, device, err := a.svc.PairingResult(r.PathValue("poll"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if token == "" {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "in attesa"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "device_id": device})
}

func (a *api) vodSubtitles(w http.ResponseWriter, r *http.Request) {
	data, err := a.svc.Subtitles(r.PathValue("session"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (a *api) details(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	d, err := a.svc.Details(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (a *api) listDownloads(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.Downloads(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) addDownload(w http.ResponseWriter, r *http.Request) {
	var in service.DownloadRequest
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	res, err := a.svc.AddDownload(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, res)
}

func (a *api) pauseDownload(w http.ResponseWriter, r *http.Request) {
	a.downloadAction(w, r, a.svc.PauseDownload)
}
func (a *api) resumeDownload(w http.ResponseWriter, r *http.Request) {
	a.downloadAction(w, r, a.svc.ResumeDownload)
}
func (a *api) deleteDownload(w http.ResponseWriter, r *http.Request) {
	a.downloadAction(w, r, a.svc.DeleteDownload)
}

func (a *api) downloadAction(w http.ResponseWriter, r *http.Request, do func(context.Context, int64) error) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := do(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) getDownloadSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.DownloadSettings(r.Context()))
}

func (a *api) putDownloadSettings(w http.ResponseWriter, r *http.Request) {
	var in service.DownloadSettings
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetDownloadSettings(r.Context(), in); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.svc.DownloadSettings(r.Context()))
}

// getUpdate: the newest release known to the server (also to devices: the
// Android app updates itself from it).
func (a *api) getUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.Update(r.Context()))
}

func (a *api) checkUpdate(w http.ResponseWriter, r *http.Request) {
	info, err := a.svc.CheckUpdate(r.Context())
	if err != nil {
		a.fail(w, r, service.Invalid("%s", info.Error))
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *api) putUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool  `json:"enabled"`
		Beta    *bool `json:"beta"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	info, err := a.svc.SetUpdatesEnabled(r.Context(), in.Enabled, in.Beta)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (a *api) getMetadataSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.MetadataSettings(r.Context()))
}

func (a *api) putMetadataSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TMDBKey string `json:"tmdb_key"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetTMDBKey(r.Context(), in.TMDBKey); err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a.svc.MetadataSettings(r.Context()))
}

func (a *api) guide(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	from := time.Now()
	if v, err := strconv.ParseInt(q.Get("from"), 10, 64); err == nil && v > 0 {
		from = time.Unix(v, 0)
	}
	hours, _ := strconv.Atoi(q.Get("hours"))
	rows, err := a.svc.Guide(r.Context(), from, hours, q.Get("section"), q.Get("list") == "favorites")
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (a *api) guideSources(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.GuideSources(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) addGuideSource(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, URL string }
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	id, err := a.svc.AddGuideSource(r.Context(), in.Name, in.URL)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

func (a *api) patchGuideSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in service.GuideSourcePatch
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.UpdateGuideSource(r.Context(), id, in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) deleteGuideSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.DeleteGuideSource(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) syncGuideSource(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	res, err := a.svc.SyncGuideSource(r.Context(), id)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *api) protectRecording(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Protected bool `json:"protected"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetRecordingProtected(r.Context(), id, in.Protected); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) recordingSpace(w http.ResponseWriter, r *http.Request) {
	sp, err := a.svc.Space(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sp)
}

func (a *api) putRecordingSettings(w http.ResponseWriter, r *http.Request) {
	var in service.RecordingSettings
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetRecordingSettings(r.Context(), in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) recordingRules(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.RecordingRules(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) addRecordingRule(w http.ResponseWriter, r *http.Request) {
	var in service.RuleInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	rule, err := a.svc.AddRecordingRule(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (a *api) previewRecordingRule(w http.ResponseWriter, r *http.Request) {
	var in service.RuleInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	list, err := a.svc.PreviewRuleInput(r.Context(), in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) putRecordingRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in service.RuleInput
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	rule, err := a.svc.UpdateRecordingRule(r.Context(), id, in)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (a *api) deleteRecordingRule(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.DeleteRecordingRule(r.Context(), id); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) startTimeshift(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Viewer string `json:"viewer"`
		Force  bool   `json:"force"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	sess, err := a.svc.StartTimeshift(ctx, id, in.Viewer, in.Force)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

func (a *api) timeshiftFile(w http.ResponseWriter, r *http.Request) {
	path, err := a.svc.TimeshiftFile(r.PathValue("session"), r.PathValue("file"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		http.NotFound(w, r) // a segment already out of the window
		return
	}
	if err != nil {
		a.fail(w, r, err)
		return
	}
	if strings.HasSuffix(path, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "private, max-age=3600")
	}
	w.Write(data)
}

func (a *api) timeshiftKeepAlive(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.KeepTimeshift(r.PathValue("session")); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) timeshiftStop(w http.ResponseWriter, r *http.Request) {
	a.svc.StopTimeshift(r.PathValue("session"))
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) getLiveSettings(w http.ResponseWriter, r *http.Request) {
	ls, err := a.svc.LiveSettings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, ls)
}

func (a *api) putLiveSettings(w http.ResponseWriter, r *http.Request) {
	var in service.LiveSettings
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetLiveSettings(r.Context(), in); err != nil {
		a.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) catchupSession(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	var in struct {
		Viewer string `json:"viewer"`
		Start  int64  `json:"start"`
		Stop   int64  `json:"stop"`
		Title  string `json:"title"`
	}
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	sess, err := a.svc.NewCatchupSession(ctx, id, time.Unix(in.Start, 0), time.Unix(in.Stop, 0), in.Viewer, in.Title)
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type countingReader struct {
	io.ReadCloser
	n   int64
	err error
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.ReadCloser.Read(p)
	c.n += int64(n)
	if err != nil {
		c.err = err
	}
	return n, err
}

func (c *countingReader) end() string {
	switch {
	case c.err == nil:
		return "chiusa dal browser"
	case c.err == io.EOF:
		return "completa"
	default:
		return c.err.Error()
	}
}

// playerLog records what a player reports (events of the video element),
// to diagnose playback problems on devices the server cannot see.
func (a *api) playerLog(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Event  string         `json:"event"`
		Detail map[string]any `json:"detail"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in); err != nil || len(in.Event) > 40 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	id := identity(r)
	a.log.Info("player", "dispositivo", id.DeviceID, "evento", in.Event, "dettagli", in.Detail)
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) browseCategories(w http.ResponseWriter, r *http.Request) {
	list, err := a.svc.BrowseCategories(r.Context(), r.PathValue("kind"))
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (a *api) getCommercials(w http.ResponseWriter, r *http.Request) {
	c, found, err := a.svc.CommercialSettings(r.Context())
	if err != nil {
		a.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": c.Enabled, "comskip": found})
}

func (a *api) putCommercials(w http.ResponseWriter, r *http.Request) {
	var in service.CommercialSettings
	if err := decode(r, &in); err != nil {
		a.fail(w, r, err)
		return
	}
	if err := a.svc.SetCommercials(r.Context(), in); err != nil {
		a.fail(w, r, err)
		return
	}
	a.getCommercials(w, r)
}
