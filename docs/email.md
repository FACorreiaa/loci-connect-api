# Email Verification & Auth Flow

## Overview
This document outlines the architecture for a "Code-based" email verification flow using SendGrid or Mailgun, replacing magic links with a copy-paste OTP (One Time Password) approach for better UX on mobile/web.

## New Auth Flow
1.  **Register**: User enters Email/Password.
2.  **Generate Code**: Server generates a 6-digit numeric code (e.g., `123456`) instead of a long UUID.
3.  **Send Email**: Use SendGrid/Mailgun to send a branded HTML email with the code.
4.  **Enter Code**: User is redirected to a Verification View to input the 6-digit code.
5.  **Verify**: Client calls `VerifyEmail(code)`.
6.  **Login**: On success, user is logged in (session created).

## Implementation Details

### 1. Token Generation Update
Modify `internal/domain/auth/service/token_manager.go`:
- Change `GenerateVerificationToken()` to generate a 6-digit number string.
- Hash logic in DB remains the same (store hash of the code), OR since it's short, store as-is with short expiry (15 mins) for UX.
- **Result**: `123456`.

### 2. Proto Requirements
`auth.proto` is ready for the basics (`VerifyEmail` isn't an RPC yet, it's often an HTTP handler, but strictly we should add it if we want full RPC).

**Proposed Proto Addition**:
```protobuf
// Add to AuthService
rpc VerifyEmail(VerifyEmailRequest) returns (LoginResponse);

message VerifyEmailRequest {
  string code = 1;
}
```
*Note*: Returning `LoginResponse` allows auto-login after verification.

### 3. Email Provider Integration
Use `github.com/sendgrid/sendgrid-go`.
- **API Key**: Store in environment variables.
- **Templates**: Use SendGrid Dynamic Templates for professional "Loci" branding.
- **Fallback**: Log code to console in Dev mode.

### 4. Client Changes
- **New View**: `src/routes/auth/verify.tsx`.
    - Input: 6 separate boxes or single input for 6 digits.
    - Action: `client.verifyEmail({ code })`.
- **Register Flow**:
    - `register()` call -> Success -> Redirect to `/auth/verify`.

## Security
- **Rate Limit**: Limit verification attempts (e.g., 5 attempts per 15 mins) to prevent brute-forcing the 6-digit code.
- **Expiry**: Codes expire in 15-30 minutes.
