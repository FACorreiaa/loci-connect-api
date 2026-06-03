# ADR 0001 — Use Stripe (direct) for web payments; defer RevenueCat to mobile

- Status: Accepted
- Date: 2026-06-03

## Context
Loci's paid product currently ships on the **web** (Solid.js client + Go API).
The payment domain is already built around **Stripe** directly: checkout
sessions, customer portal, subscriptions, invoices, refunds, and a webhook
handler (`/webhooks/stripe`). We evaluated whether to adopt **RevenueCat** as the
billing layer instead.

## Decision
Keep **Stripe direct** for the web app. Do **not** adopt RevenueCat now.

## Rationale
- **Right tool for web.** Stripe is the strongest option for a browser/B2B
  product: broadest payment methods, mature subscriptions + usage metering,
  first-class webhooks. All-in cost ≈ 2.9% + $0.30 + 0.7% billing ≈ ~3.6%.
- **RevenueCat's value is mobile.** RevenueCat sits on top of Apple StoreKit /
  Google Play Billing, normalizes them, and gives cross-platform entitlements +
  mobile analytics. For a web-only product it adds a 1% fee (above $2.5k MTR) and
  an extra layer for **no web benefit**.
- **Already integrated.** Switching now would be rework with negative ROI.

## When to revisit
Adopt RevenueCat **when native iOS/Android apps ship** and we must sell the same
subscription through Apple/Google IAP. Then run **both**: Stripe for web,
RevenueCat for mobile, reconciled on a single user record. RevenueCat's Web
Billing (Stripe under the hood) is only interesting if we want one entitlement
system spanning web + mobile at that point.

## Consequences
- Continue investing in the Stripe integration (price→plan mapping, webhook
  robustness, customer portal).
- Subscription plans map by Stripe price **interval**: `month` →
  `premium_monthly`, `year` → `premium_annual` (see `payment/service.go`).

## Sources
- [Stripe Billing vs RevenueCat: when each one wins (2026)](https://adamarant.com/en/blog/stripe-billing-vs-revenuecat-picking-the-right-billing-layer)
- [RevenueCat Web Billing overview](https://www.revenuecat.com/docs/web/web-billing/overview)
- [RevenueCat pricing](https://www.revenuecat.com/billing)
