-- Phase 4 (decisions/cleanup web UI) added these two lookups against
-- tables 0001 already created: decisions_status backs PendingDecisions'
-- status='pending' filter, remediations_created backs RecentRemediations'
-- created_at DESC sort/filter. Both hosts had already reached schema v1
-- before this migration was written, so they belong here rather than in
-- 0001.
CREATE INDEX decisions_status ON decisions (status, requested_at DESC);
CREATE INDEX remediations_created ON remediations (created_at DESC);
