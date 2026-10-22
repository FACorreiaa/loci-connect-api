package achievements

import (
	"context"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/boards/v1/boardsv1connect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/bundle/v1/bundlev1connect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/itinerary/itineraryconnect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/journal/journalconnect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/list/listv1connect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/ranking/rankingconnect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/review/reviewv1connect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/social/socialconnect"
	"github.com/FACorreiaa/loci-connect-proto/v5/gen/go/loci/trip/tripconnect"
	"github.com/google/uuid"

	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
)

// touchedBy are the calls that can earn an achievement without earning
// points, so no award evaluates them. Points-earning actions are evaluated
// by the award itself.
var touchedBy = map[string]bool{
	journalconnect.JournalServiceSetDayDiaryProcedure:        true,
	journalconnect.JournalServiceConfirmPhotoProcedure:       true,
	reviewv1connect.ReviewServiceCreateReviewProcedure:       true,
	boardsv1connect.BoardsServiceCreatePostProcedure:         true,
	boardsv1connect.BoardsServiceCreateCommentProcedure:      true,
	bundlev1connect.BundleServiceClaimBundleProcedure:        true,
	rankingconnect.RankingServiceRatePlaceProcedure:          true,
	rankingconnect.RankingServiceComparePlacesProcedure:      true,
	listv1connect.ListServiceAddListItemProcedure:            true,
	itineraryconnect.ItineraryServiceAddListItemProcedure:    true,
	tripconnect.TripServiceCopyTripProcedure:                 true,
	socialconnect.SocialServiceFollowProcedure:               true,
	socialconnect.SocialServiceRespondFollowRequestProcedure: true,
	socialconnect.SocialServiceRespondFriendRequestProcedure: true,
	socialconnect.SocialServiceSendFriendRequestProcedure:    true,
	socialconnect.SocialServiceAcceptInviteProcedure:         true,
}

// Interceptor evaluates the caller after a successful call that can earn an
// achievement without points, off the request path. It must run after auth.
// What it misses (a helpful vote on someone's review, an invite accepted)
// is caught on that user's next award or when they open their list.
func (s *Service) Interceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			if err != nil || s == nil || !s.enabled || !touchedBy[req.Spec().Procedure] {
				return res, err
			}
			if raw, ok := interceptors.GetUserIDFromContext(ctx); ok {
				if id, perr := uuid.Parse(raw); perr == nil {
					s.Touch(ctx, id)
				}
			}
			return res, err
		}
	}
}
