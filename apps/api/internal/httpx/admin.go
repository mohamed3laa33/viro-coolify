package httpx

import (
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/domain"
	"github.com/mohamed3laa33/viro-coolify/apps/api/internal/store"
)

// ---- Plans ----

func (s *Server) handleAdminListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlans(r.Context())
	if err != nil {
		s.logger.Error("admin list plans", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list plans")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": plans})
}

func (s *Server) handleAdminCreatePlan(w http.ResponseWriter, r *http.Request) {
	var p domain.Plan
	if !decodeJSON(w, r, &p) {
		return
	}
	p.ID = strings.TrimSpace(p.ID)
	if p.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if _, err := s.store.GetPlan(r.Context(), p.ID); err == nil {
		writeError(w, http.StatusConflict, "plan already exists")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error("admin create plan", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}
	if err := s.store.UpsertPlan(r.Context(), &p); err != nil {
		s.logger.Error("admin create plan", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create plan")
		return
	}
	s.audit(r.Context(), "", "plan.create", "plan", p.ID, "")
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleAdminUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.store.GetPlan(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "plan not found")
		return
	}
	if err != nil {
		s.logger.Error("admin update plan", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load plan")
		return
	}
	// Decode the patch onto the existing record (omitted fields are preserved).
	plan := *existing
	if !decodeJSON(w, r, &plan) {
		return
	}
	plan.ID = id // id is immutable
	if err := s.store.UpsertPlan(r.Context(), &plan); err != nil {
		s.logger.Error("admin update plan", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to update plan")
		return
	}
	s.audit(r.Context(), "", "plan.update", "plan", plan.ID, "")
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleAdminDeletePlan(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeletePlan(r.Context(), chi.URLParam(r, "id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "plan not found")
		return
	}
	if err != nil {
		s.logger.Error("admin delete plan", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete plan")
		return
	}
	s.audit(r.Context(), "", "plan.delete", "plan", chi.URLParam(r, "id"), "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Pricing components (hourly prices, admin-set) ----

func (s *Server) handleAdminListPricing(w http.ResponseWriter, r *http.Request) {
	comps, err := s.store.ListPricingComponents(r.Context())
	if err != nil {
		s.logger.Error("admin list pricing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list pricing")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": comps})
}

func (s *Server) handleAdminCreatePricing(w http.ResponseWriter, r *http.Request) {
	var p domain.PricingComponent
	if !decodeJSON(w, r, &p) {
		return
	}
	p.Key = strings.TrimSpace(p.Key)
	if p.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}
	if p.PricePerHour < 0 {
		writeError(w, http.StatusBadRequest, "pricePerHour must not be negative")
		return
	}
	if _, err := s.store.GetPricingComponent(r.Context(), p.Key); err == nil {
		writeError(w, http.StatusConflict, "pricing component already exists")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error("admin create pricing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create pricing")
		return
	}
	if err := s.store.UpsertPricingComponent(r.Context(), &p); err != nil {
		s.logger.Error("admin create pricing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create pricing")
		return
	}
	s.audit(r.Context(), "", "pricing.create", "pricing", p.Key, "")
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleAdminUpdatePricing(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	existing, err := s.store.GetPricingComponent(r.Context(), key)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "pricing component not found")
		return
	}
	if err != nil {
		s.logger.Error("admin update pricing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load pricing")
		return
	}
	comp := *existing
	if !decodeJSON(w, r, &comp) {
		return
	}
	comp.Key = key // key is immutable
	if comp.PricePerHour < 0 {
		writeError(w, http.StatusBadRequest, "pricePerHour must not be negative")
		return
	}
	if err := s.store.UpsertPricingComponent(r.Context(), &comp); err != nil {
		s.logger.Error("admin update pricing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to update pricing")
		return
	}
	s.audit(r.Context(), "", "pricing.update", "pricing", comp.Key, "")
	writeJSON(w, http.StatusOK, comp)
}

func (s *Server) handleAdminDeletePricing(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeletePricingComponent(r.Context(), chi.URLParam(r, "key"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "pricing component not found")
		return
	}
	if err != nil {
		s.logger.Error("admin delete pricing", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete pricing")
		return
	}
	s.audit(r.Context(), "", "pricing.delete", "pricing", chi.URLParam(r, "key"), "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Service templates ----

func (s *Server) handleAdminListTemplates(w http.ResponseWriter, r *http.Request) {
	tmpls, err := s.store.ListServiceTemplates(r.Context())
	if err != nil {
		s.logger.Error("admin list templates", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to list templates")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": tmpls})
}

func (s *Server) handleAdminCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var t domain.ServiceTemplate
	if !decodeJSON(w, r, &t) {
		return
	}
	t.Key = strings.TrimSpace(t.Key)
	if t.Key == "" {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}
	if _, err := s.store.GetServiceTemplate(r.Context(), t.Key); err == nil {
		writeError(w, http.StatusConflict, "template already exists")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.logger.Error("admin create template", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create template")
		return
	}
	if err := s.store.UpsertServiceTemplate(r.Context(), &t); err != nil {
		s.logger.Error("admin create template", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to create template")
		return
	}
	s.audit(r.Context(), "", "template.create", "template", t.Key, "")
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleAdminUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "key")
	existing, err := s.store.GetServiceTemplate(r.Context(), key)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		s.logger.Error("admin update template", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load template")
		return
	}
	tmpl := *existing
	if !decodeJSON(w, r, &tmpl) {
		return
	}
	tmpl.Key = key // key is immutable
	if err := s.store.UpsertServiceTemplate(r.Context(), &tmpl); err != nil {
		s.logger.Error("admin update template", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to update template")
		return
	}
	s.audit(r.Context(), "", "template.update", "template", tmpl.Key, "")
	writeJSON(w, http.StatusOK, tmpl)
}

func (s *Server) handleAdminDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	err := s.store.DeleteServiceTemplate(r.Context(), chi.URLParam(r, "key"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "template not found")
		return
	}
	if err != nil {
		s.logger.Error("admin delete template", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to delete template")
		return
	}
	s.audit(r.Context(), "", "template.delete", "template", chi.URLParam(r, "key"), "")
	w.WriteHeader(http.StatusNoContent)
}

// ---- Platform settings ----

func (s *Server) handleAdminGetSettings(w http.ResponseWriter, r *http.Request) {
	set, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.logger.Error("admin get settings", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) handleAdminUpdateSettings(w http.ResponseWriter, r *http.Request) {
	existing, err := s.store.GetSettings(r.Context())
	if err != nil {
		s.logger.Error("admin update settings", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load settings")
		return
	}
	set := *existing
	if !decodeJSON(w, r, &set) {
		return
	}
	if err := s.store.UpdateSettings(r.Context(), &set); err != nil {
		s.logger.Error("admin update settings", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to update settings")
		return
	}
	s.audit(r.Context(), "", "settings.update", "settings", "platform", "")
	writeJSON(w, http.StatusOK, set)
}

// ---- Overview ----

type adminOverview struct {
	OrgCount            int              `json:"orgCount"`
	UserCount           int              `json:"userCount"`
	SubscriptionsByPlan map[string]int   `json:"subscriptionsByPlan"`
	UsageTotals         map[string]int64 `json:"usageTotals"`
}

func (s *Server) handleAdminOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	orgs, err := s.store.ListAllOrgs(ctx)
	if err != nil {
		s.logger.Error("admin overview", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	userCount, err := s.store.CountUsers(ctx)
	if err != nil {
		s.logger.Error("admin overview", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	subs, err := s.store.ListAllSubscriptions(ctx)
	if err != nil {
		s.logger.Error("admin overview", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}
	usageTotals, err := s.store.SumUsageByMetric(ctx)
	if err != nil {
		s.logger.Error("admin overview", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load overview")
		return
	}

	out := adminOverview{
		OrgCount:            len(orgs),
		UserCount:           userCount,
		SubscriptionsByPlan: map[string]int{},
		UsageTotals:         usageTotals,
	}
	for _, sub := range subs {
		out.SubscriptionsByPlan[sub.PlanID]++
	}
	writeJSON(w, http.StatusOK, out)
}
