package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"

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

	return connect.NewResponse(&paymentv1.GetPaymentResponse{
		Payment: paymentToProto(p),
	}), nil
}

func (h *paymentHandler) GetUserPayments(ctx context.Context, req *connect.Request[paymentv1.GetUserPaymentsRequest]) (*connect.Response[paymentv1.GetUserPaymentsResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	payments, total, err := h.service.GetUserPayments(ctx, userID, int(req.Msg.Page), int(req.Msg.PageSize))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoPayments := make([]*paymentv1.Payment, len(payments))
	for i := range payments {
		protoPayments[i] = paymentToProto(&payments[i])
	}

	return connect.NewResponse(&paymentv1.GetUserPaymentsResponse{
		Payments: protoPayments,
		Total:    int32(total),
		Page:     normalizedPage(req.Msg.Page),
		PageSize: normalizedPageSize(req.Msg.PageSize),
	}), nil
}

func (h *paymentHandler) RefundPayment(ctx context.Context, req *connect.Request[paymentv1.RefundPaymentRequest]) (*connect.Response[paymentv1.RefundPaymentResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	p, err := h.service.GetPayment(ctx, req.Msg.PaymentId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if p.UserID != userID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("access denied"))
	}

	refund, err := h.service.RefundPayment(ctx, req.Msg.PaymentId, req.Msg.AmountCents)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&paymentv1.RefundPaymentResponse{
		RefundId:      refund.ID.String(),
		PaymentId:     refund.PaymentID.String(),
		AmountCents:   refund.AmountCents,
		Currency:      refund.Currency,
		Status:        refund.Status,
		IsFullRefund:  refund.AmountCents >= p.Amount,
		TotalRefunded: refund.AmountCents,
	}), nil
}

func (h *paymentHandler) GetInvoice(ctx context.Context, req *connect.Request[paymentv1.GetInvoiceRequest]) (*connect.Response[paymentv1.Invoice], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	invoice, err := h.service.GetInvoice(ctx, req.Msg.InvoiceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if invoice.UserID != userID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("access denied"))
	}

	return connect.NewResponse(invoiceToProto(invoice)), nil
}

func (h *paymentHandler) GetUserInvoices(ctx context.Context, req *connect.Request[paymentv1.GetUserInvoicesRequest]) (*connect.Response[paymentv1.GetUserInvoicesResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	invoices, total, err := h.service.GetUserInvoices(ctx, userID, int(req.Msg.Page), int(req.Msg.PageSize))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoInvoices := make([]*paymentv1.Invoice, len(invoices))
	for i := range invoices {
		protoInvoices[i] = invoiceToProto(&invoices[i])
	}

	return connect.NewResponse(&paymentv1.GetUserInvoicesResponse{
		Invoices: protoInvoices,
		Total:    int32(total),
		Page:     normalizedPage(req.Msg.Page),
		PageSize: normalizedPageSize(req.Msg.PageSize),
	}), nil
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
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	current, err := h.service.GetSubscription(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	subscriptionID := req.Msg.SubscriptionId
	if subscriptionID == "" && current.Subscription != nil && current.Subscription.ExternalSubscriptionID != nil {
		subscriptionID = *current.Subscription.ExternalSubscriptionID
	}
	if subscriptionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("subscription_id is required"))
	}
	if current.Subscription == nil || current.Subscription.UserID != userID {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("access denied"))
	}

	err = h.service.CancelSubscription(ctx, subscriptionID, req.Msg.CancelAtPeriodEnd)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	status := "canceled"
	if req.Msg.CancelAtPeriodEnd {
		status = "active"
	}
	return connect.NewResponse(&paymentv1.CancelSubscriptionResponse{
		SubscriptionId: subscriptionID,
		Status:         status,
	}), nil
}

func (h *paymentHandler) GetUserSubscriptions(ctx context.Context, req *connect.Request[paymentv1.GetUserSubscriptionsRequest]) (*connect.Response[paymentv1.GetUserSubscriptionsResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	sub, err := h.service.GetSubscription(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if sub == nil || sub.Subscription == nil {
		return connect.NewResponse(&paymentv1.GetUserSubscriptionsResponse{}), nil
	}
	return connect.NewResponse(&paymentv1.GetUserSubscriptionsResponse{
		Subscriptions: []*paymentv1.Subscription{subscriptionToProto(sub.Subscription)},
	}), nil
}

func (h *paymentHandler) GetSubscription(ctx context.Context, req *connect.Request[paymentv1.GetSubscriptionRequest]) (*connect.Response[paymentv1.GetSubscriptionResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	sub, err := h.service.GetSubscription(ctx, userID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&paymentv1.GetSubscriptionResponse{
		Subscription: subscriptionToProto(sub.Subscription),
		Usage: &paymentv1.SubscriptionUsage{
			RequestsToday:       sub.RequestsToday,
			RequestsLimit:       sub.RequestsLimit,
			SavedLocations:      sub.SavedLocations,
			SavedLocationsLimit: sub.SavedLocationsLimit,
		},
	}), nil
}

func (h *paymentHandler) CreateCheckoutSession(ctx context.Context, req *connect.Request[paymentv1.CreateCheckoutSessionRequest]) (*connect.Response[paymentv1.CreateCheckoutSessionResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.userRepo.GetUserByID(ctx, userID)
	if err != nil {
		h.logger.Error("failed to fetch user", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to fetch user"))
	}

	// Mode from the request is intentionally ignored: checkout is
	// subscription-only; arbitrary one-time payment sessions are not offered.
	result, err := h.service.CreateCheckoutSession(ctx, &CreateCheckoutSessionParams{
		UserID:     userID,
		Email:      user.Email,
		PriceID:    req.Msg.PriceId,
		SuccessURL: req.Msg.SuccessUrl,
		CancelURL:  req.Msg.CancelUrl,
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&paymentv1.CreateCheckoutSessionResponse{
		SessionId: result.SessionID,
		Url:       result.URL,
	}), nil
}

func (h *paymentHandler) CreateCustomerPortalSession(ctx context.Context, req *connect.Request[paymentv1.CreateCustomerPortalSessionRequest]) (*connect.Response[paymentv1.CreateCustomerPortalSessionResponse], error) {
	userID, err := h.authenticatedUserID(ctx)
	if err != nil {
		return nil, err
	}

	result, err := h.service.CreateCustomerPortalSession(ctx, userID, req.Msg.ReturnUrl)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&paymentv1.CreateCustomerPortalSessionResponse{
		SessionId: result.SessionID,
		Url:       result.URL,
	}), nil
}

func (h *paymentHandler) authenticatedUserID(ctx context.Context) (uuid.UUID, error) {
	userIDStr, ok := interceptors.GetUserIDFromContext(ctx)
	if !ok || userIDStr == "" {
		return uuid.Nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return uuid.Nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	return userID, nil
}

func normalizedPage(page int32) int32 {
	if page < 1 {
		return 1
	}
	return page
}

func normalizedPageSize(pageSize int32) int32 {
	if pageSize < 1 {
		return 10
	}
	if pageSize > 100 {
		return 100
	}
	return pageSize
}

func paymentToProto(p *Payment) *paymentv1.Payment {
	if p == nil {
		return nil
	}
	protoPayment := &paymentv1.Payment{
		Id:        p.ID.String(),
		UserId:    p.UserID.String(),
		Provider:  p.Provider,
		Type:      p.Type,
		Amount:    p.Amount,
		Currency:  p.Currency,
		Status:    p.Status,
		CreatedAt: timeToTimestamp(p.CreatedAt),
		UpdatedAt: timeToTimestamp(p.UpdatedAt),
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
	return protoPayment
}

func invoiceToProto(i *Invoice) *paymentv1.Invoice {
	if i == nil {
		return nil
	}
	protoInvoice := &paymentv1.Invoice{
		Id:       i.ID.String(),
		UserId:   i.UserID.String(),
		Amount:   i.Amount,
		Currency: i.Currency,
		Status:   i.Status,
		IssuedAt: timePtrToTimestamp(i.IssuedAt),
		PaidAt:   timePtrToTimestamp(i.PaidAt),
	}
	if i.PaymentID != nil {
		protoInvoice.PaymentId = i.PaymentID.String()
	}
	if i.InvoiceNumber != nil {
		protoInvoice.InvoiceNumber = *i.InvoiceNumber
	}
	if i.PdfURL != nil {
		protoInvoice.PdfUrl = *i.PdfURL
	}
	return protoInvoice
}

func subscriptionToProto(s *Subscription) *paymentv1.Subscription {
	if s == nil {
		return nil
	}
	protoSub := &paymentv1.Subscription{
		Id:                 s.ID.String(),
		UserId:             s.UserID.String(),
		PlanId:             s.Plan,
		Status:             s.Status,
		CurrentPeriodStart: timeToTimestamp(s.StartDate),
		CurrentPeriodEnd:   timePtrToTimestamp(s.EndDate),
	}
	if s.ExternalSubscriptionID != nil {
		protoSub.StripeSubscriptionId = *s.ExternalSubscriptionID
	}
	if s.ExternalCustomerID != nil {
		protoSub.StripeCustomerId = *s.ExternalCustomerID
	}
	return protoSub
}

func timeToTimestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func timePtrToTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil || t.IsZero() {
		return nil
	}
	return timestamppb.New(*t)
}
