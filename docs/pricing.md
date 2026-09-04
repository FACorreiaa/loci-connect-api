# Pricing and Subscription Architecture

## Overview
This document outlines the implementation strategy for the new pricing tiers and subscription model. The system currently supports a basic subscription schema (`subscriptions` table) which will be leveraged to enforce rate limits and feature access.

## Tiers & Limits

Two public tiers. "Pro" is the marketing name for both `premium_monthly` and
`premium_annual` plan values; Pro is sold as unlimited but carries a hidden
fair-use cap. Limits are env-configurable (`FREE_DAILY_LLM_LIMIT`, default 10;
`PRO_DAILY_LLM_LIMIT`, default 100).

| Tier | Limits | Notes |
|------|--------|-------|
| **Admin** (`ADMIN_EMAIL` env) | Unlimited | Quota bypass |
| **Free** | 10 LLM requests/day | Metered on chat RPCs only (StartChat, ContinueChat, StreamChat) |
| **Pro** (`premium_monthly` / `premium_annual`) | "Unlimited" — hidden fair-use cap 100/day | Fair-use denial shows retry copy, never an upgrade CTA |

Historic tier table (superseded): free 5/day, paid 10/day, premium unlimited.
The `paid`/`explorer` plan strings never existed in the DB enum and were removed.

## Implementation Strategy

### 1. Database Schema
Existing `subscriptions` table (`pkg/db/migrations/0002_create_users.up.sql`) will be used.
- `plan`: Enum will need to support `free`, `paid`, `premium`.
- `user_id`: Links to `users` table.
- `status`: `active`, `past_due`, `canceled`.

**New Requirement**: Usage Tracking
We need to track "requests per day".
- **Option A (Redis)**: fast, ephemeral. Keys: `usage:requests:{user_id}:{date}`.
- **Option B (Postgres)**: `daily_usage` table.
   ```sql
   CREATE TABLE user_daily_usage (
       user_id UUID,
       date DATE DEFAULT CURRENT_DATE,
       request_count INT DEFAULT 0,
       PRIMARY KEY (user_id, date)
   );
   ```
*Recommendation*: Start with **Redis** for speed, or **Postgres** if keeping stack simple is priority. Given the current stack uses Postgres heavily, a simple table is easiest to maintain initially. We will do this with Postgres for now. 

### 2. Service Layer Logic (`ChatService`)
Access control will be enforced in `internal/domain/chat/service/chat_service.go`.
or, even better, we should create an interceptor for this and create a new domain for it. create
#### Rate Limiting Logic
We should create an interceptor for this. 
Before processing a chat request:
1.  **Check User Tier**: Query `subscriptions` table (or cached in context/JWT).
    *   If Email == `loci122025@gmail.com` -> Bypass all checks (Admin, should be an environment variable).
2.  **Check Daily Limit**:
    *   Query `user_daily_usage`.
    *   If `request_count >= limit` -> Return `RESOURCE_EXHAUSTED` (Quota Exceeded).
3.  **Select Model**:
    *   Free: Use `gemini-1.5-flash`.
    *   Paid/Premium: Use `gemini-1.5-pro`.
4.  **Increment Usage**:
    *   On successful request start, increment `request_count`.

We should handle errors gracefully and return a proper error message to the user when he hits the limit.

#### Feature Gating
- **Multi-City**: Check if `Tier == Premium` (or Admin).
- **List Creation**:
    *   Check `lists_created` count in `UserStats` (from `user.proto`).
    *   If `lists_created >= 5` AND `Tier != Premium` -> Block creation.

### 3. Client Implementation
- **State**: Store `subscription_plan` in `AuthContext` (it's already in `Claims`).
- **UI**:
    *   Show current usage (e.g., "3/5 free requests used").
    *   Disable/Lock features (e.g., "Multi-city" toggle disabled with tooltip "Upgrade to Premium").
    *   Redirect to `/pricing` on limit hit.

## Pricing Page
We should create a pricing page that shows the different tiers and their features.
We already have a pricing page but should be adapted for this tiers. use or improve the same styling with our glassy design and use the components from shadcn inside the ui folder. 

## Stripe Integration 
1.  **Webhooks**: Listen for `checkout.session.completed` to update `subscriptions` table.
2.  **Customer Portal**: Use Stripe Customer Portal for managing subscriptions.
3.  **Metadata**: Store `user_id` in Stripe `client_reference_id` to link callbacks.

- In the end the user should be able to make one time payments of the tiers (and I will have extra packs for sale as well in the future) and subscribe for plans too, with stripe. Stripe webhook should make all the necessary changes about when a user makes a subscription, cancels a subcription and handles refunds and payments details. 