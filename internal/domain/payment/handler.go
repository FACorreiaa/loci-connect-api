package payment

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/FACorreiaa/loci-connect-api/internal/domain/user"
	"github.com/FACorreiaa/loci-connect-api/pkg/interceptors"
	paymentv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/payment/v1"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/payment/v1/paymentv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type paymentHandler struct {
	service  Service
	userRepo user.UserRepo
	logger   *slog.Logger
	paymentv1connect.UnimplementedPaymentServiceHandler
}

func NewPaymentServiceHandler(service Service, userRepo user.UserRepo, logger *slog.Logger) paymentv1connect.PaymentServiceHandler {
	return &paymentHandler{
		service:  service,
		userRepo: userRepo,
		logger:   logger,
	}
}

func (h *paymentHandler) CreatePayment(ctx context.Context, req *connect.Request[paymentv1.CreatePaymentRequest]) (*connect.Response[paymentv1.CreatePaymentResponse], error) {
	userID, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userID == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	// Convert proto metadata to map[string]string
	meta := req.Msg.Metadata

	params := &CreatePaymentParams{
		UserID:      userID,
		Amount:      req.Msg.Amount,
		Currency:    req.Msg.Currency,
		Type:        req.Msg.Type,
		Description: req.Msg.Description,
		Metadata:    meta,
	}

	result, err := h.service.CreatePayment(ctx, params)
	if err != nil {
		h.logger.Error("failed to create payment", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	res := connect.NewResponse(&paymentv1.CreatePaymentResponse{
		PaymentId:         result.PaymentID,
		ClientSecret:      result.ClientSecret,
		ExternalPaymentId: result.ExternalPaymentID,
		Status:            result.Status,
	})
	return res, nil
}

func (h *paymentHandler) GetPayment(ctx context.Context, req *connect.Request[paymentv1.GetPaymentRequest]) (*connect.Response[paymentv1.GetPaymentResponse], error) {
	// Check auth
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	p, err := h.service.GetPayment(ctx, req.Msg.PaymentId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Verify ownership
	if p.UserID.String() != userIDStr {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("access denied"))
	}

	// Convert domain model to proto
	protoPayment := &paymentv1.Payment{
		Id:          p.ID.String(),
		UserId:      p.UserID.String(),
		Provider:    p.Provider,
		Type:        p.Type,
		Amount:      p.Amount,
		Currency:    p.Currency,
		Status:      p.Status,
		Description: "", // handle nil
		CreatedAt:   timestamppb.New(p.CreatedAt),
		UpdatedAt:   timestamppb.New(p.UpdatedAt),
	}
	if p.ExternalPaymentID != nil {
		protoPayment.ExternalPaymentId = *p.ExternalPaymentID
	}
	if p.Description != nil {
		protoPayment.Description = *p.Description
	}
	if p.PaymentMethod != nil {
		protoPayment.PaymentMethod = *p.PaymentMethod
	}

	// TODO: Add Invoice fetching if needed (service.GetPayment currently only returns Payment)

	return connect.NewResponse(&paymentv1.GetPaymentResponse{
		Payment: protoPayment,
	}), nil
}

// TODO: Implement other methods (GetUserPayments, RefundPayment, etc.)

func (h *paymentHandler) GetUserPayments(ctx context.Context, req *connect.Request[paymentv1.GetUserPaymentsRequest]) (*connect.Response[paymentv1.GetUserPaymentsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *paymentHandler) RefundPayment(ctx context.Context, req *connect.Request[paymentv1.RefundPaymentRequest]) (*connect.Response[paymentv1.RefundPaymentResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *paymentHandler) GetInvoice(ctx context.Context, req *connect.Request[paymentv1.GetInvoiceRequest]) (*connect.Response[paymentv1.Invoice], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *paymentHandler) GetUserInvoices(ctx context.Context, req *connect.Request[paymentv1.GetUserInvoicesRequest]) (*connect.Response[paymentv1.GetUserInvoicesResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *paymentHandler) CreateSubscription(ctx context.Context, req *connect.Request[paymentv1.CreateSubscriptionRequest]) (*connect.Response[paymentv1.CreateSubscriptionResponse], error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}

	// Fetch user for Email
	user, err := h.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		h.logger.Error("failed to fetch user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to fetch user"))
	}

	params := &CreateSubscriptionParams{
		UserID:          userIDStr,
		Email:           user.Email,
		PlanID:          req.Msg.PlanId,
		PaymentMethodID: req.Msg.PaymentMethodId, // Optional
	}

	result, err := h.service.CreateSubscription(ctx, params)
	if err != nil {
		h.logger.Error("failed to create subscription", "error", err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&paymentv1.CreateSubscriptionResponse{
		SubscriptionId: result.SubscriptionID,
		ClientSecret:   result.ClientSecret,
		Status:         result.Status,
	}), nil
}

func (h *paymentHandler) CancelSubscription(ctx context.Context, req *connect.Request[paymentv1.CancelSubscriptionRequest]) (*connect.Response[paymentv1.CancelSubscriptionResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}

func (h *paymentHandler) GetUserSubscriptions(ctx context.Context, req *connect.Request[paymentv1.GetUserSubscriptionsRequest]) (*connect.Response[paymentv1.GetUserSubscriptionsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("not implemented"))
}
