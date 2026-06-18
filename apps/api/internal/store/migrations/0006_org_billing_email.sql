-- 0006_org_billing_email: organizations gain an editable billing email, surfaced
-- by the org settings UI via PATCH /v1/orgs/{orgId}. It is optional (defaults to
-- empty) so existing orgs migrate cleanly without a value.

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS billing_email text NOT NULL DEFAULT '';
