package chatbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	locitypes "github.com/FACorreiaa/loci-connect-api/internal/types"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// Page renders one page of an answer this user has already been given.
//
// It reads through the same service method the Connect handler calls, so a
// page in a chat and a page in the app are the same places in the same order.
// Nothing here generates: the answer was produced once, and paging it is a
// read of what that generation wrote.
func (a *Answerer) Page(ctx context.Context, userID uuid.UUID, email, token string) (string, string, error) {
	if a.chat == nil {
		return "", "", errors.New("chatbridge: no chat service configured")
	}

	sessionID, page, section, ok := decodePage(token)
	if !ok {
		// Rejected rather than defaulted to page one: a default would be page
		// one of whatever session id happened to parse out of a bad token.
		return "", "", fmt.Errorf("chatbridge: unreadable page token %q", token)
	}

	// Same claims injection, same reason, as Answer: without it the request
	// below would not know whose it is. The ownership check itself does not
	// depend on this — GetSessionPOIs takes the user id as an argument — which
	// is the safer arrangement.
	ctx = interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String(), Email: email})

	result, err := a.chat.GetSessionPOIs(ctx, userID, sessionID, section, page, maxRenderedPOIs)
	if err != nil {
		return "", "", fmt.Errorf("chatbridge: %w", err)
	}
	if result == nil || len(result.POIs) == 0 {
		return "That is everything I found. Ask me for somewhere else and I will start again.", "", nil
	}

	from := (result.Page-1)*result.PageSize + 1
	to := from + len(result.POIs) - 1

	var b strings.Builder
	fmt.Fprintf(&b, "More places (%d–%d of %d)\n\n", from, to, result.Total)
	writePOIs(&b, byDistance(result.POIs))

	next := ""
	if result.HasMore {
		next = encodePage(sessionID, result.Page+1, result.Section)
	}
	return strings.TrimSpace(b.String()), next, nil
}

// LatestPage renders a page of this user's most recent answer.
//
// It is what a typed "/more" resolves to. There is no token to decode, so the
// session comes from the same lookup a follow-up message uses — the most recent
// conversation is the one "more" refers to — and the section is the plan.
func (a *Answerer) LatestPage(ctx context.Context, userID uuid.UUID, email string, page int) (string, string, error) {
	if a.chat == nil {
		return "", "", errors.New("chatbridge: no chat service configured")
	}
	ctx = interceptors.ContextWithClaims(ctx, &interceptors.Claims{UserID: userID.String(), Email: email})

	sessionID, ok := a.latestSession(ctx, userID)
	if !ok {
		return "Ask me for somewhere first and I will have something to show more of.", "", nil
	}
	// Through the token path, so a typed /more and a pressed button render
	// identically rather than by two nearly-identical code paths.
	return a.Page(ctx, userID, email, encodePage(sessionID, page, locitypes.SectionItinerary))
}
