package export

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"

	exportv1 "github.com/FACorreiaa/loci-connect-proto/gen/go/loci/export"
	"github.com/FACorreiaa/loci-connect-proto/gen/go/loci/export/exportv1connect"
)

// Handler implements the ExportService
type Handler struct {
	exportv1connect.UnimplementedExportServiceHandler
	pdfGen *PDFGenerator
	logger *slog.Logger
}

// NewHandler creates a new export handler
func NewHandler(logger *slog.Logger) *Handler {
	return &Handler{
		pdfGen: NewPDFGenerator(),
		logger: logger.With(slog.String("component", "export-handler")),
	}
}

// ExportPOIsToPDF exports POIs to PDF
func (h *Handler) ExportPOIsToPDF(
	ctx context.Context,
	req *connect.Request[exportv1.ExportPOIsRequest],
) (*connect.Response[exportv1.ExportPDFResponse], error) {
	l := h.logger.With(slog.String("method", "ExportPOIsToPDF"))
	l.InfoContext(ctx, "exporting POIs to PDF", slog.Int("count", len(req.Msg.Pois)))

	if len(req.Msg.Pois) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no POIs to export"))
	}

	pdfData, err := h.pdfGen.GeneratePOIsPDF(req.Msg.Pois, req.Msg.Title)
	if err != nil {
		l.ErrorContext(ctx, "failed to generate PDF", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate PDF: %w", err))
	}

	filename := fmt.Sprintf("places_%s.pdf", time.Now().Format("2006-01-02"))

	return connect.NewResponse(&exportv1.ExportPDFResponse{
		PdfData:     pdfData,
		Filename:    filename,
		ContentType: "application/pdf",
		SizeBytes:   int64(len(pdfData)),
	}), nil
}

// ExportHotelsToPDF exports hotels to PDF
func (h *Handler) ExportHotelsToPDF(
	ctx context.Context,
	req *connect.Request[exportv1.ExportHotelsRequest],
) (*connect.Response[exportv1.ExportPDFResponse], error) {
	l := h.logger.With(slog.String("method", "ExportHotelsToPDF"))
	l.InfoContext(ctx, "exporting hotels to PDF", slog.Int("count", len(req.Msg.Hotels)))

	if len(req.Msg.Hotels) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no hotels to export"))
	}

	pdfData, err := h.pdfGen.GenerateHotelsPDF(req.Msg.Hotels, req.Msg.Title, req.Msg.CityName)
	if err != nil {
		l.ErrorContext(ctx, "failed to generate PDF", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate PDF: %w", err))
	}

	filename := fmt.Sprintf("hotels_%s.pdf", time.Now().Format("2006-01-02"))

	return connect.NewResponse(&exportv1.ExportPDFResponse{
		PdfData:     pdfData,
		Filename:    filename,
		ContentType: "application/pdf",
		SizeBytes:   int64(len(pdfData)),
	}), nil
}

// ExportRestaurantsToPDF exports restaurants to PDF
func (h *Handler) ExportRestaurantsToPDF(
	ctx context.Context,
	req *connect.Request[exportv1.ExportRestaurantsRequest],
) (*connect.Response[exportv1.ExportPDFResponse], error) {
	l := h.logger.With(slog.String("method", "ExportRestaurantsToPDF"))
	l.InfoContext(ctx, "exporting restaurants to PDF", slog.Int("count", len(req.Msg.Restaurants)))

	if len(req.Msg.Restaurants) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no restaurants to export"))
	}

	pdfData, err := h.pdfGen.GenerateRestaurantsPDF(req.Msg.Restaurants, req.Msg.Title, req.Msg.CityName)
	if err != nil {
		l.ErrorContext(ctx, "failed to generate PDF", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate PDF: %w", err))
	}

	filename := fmt.Sprintf("restaurants_%s.pdf", time.Now().Format("2006-01-02"))

	return connect.NewResponse(&exportv1.ExportPDFResponse{
		PdfData:     pdfData,
		Filename:    filename,
		ContentType: "application/pdf",
		SizeBytes:   int64(len(pdfData)),
	}), nil
}

// ExportActivitiesToPDF exports activities to PDF
func (h *Handler) ExportActivitiesToPDF(
	ctx context.Context,
	req *connect.Request[exportv1.ExportActivitiesRequest],
) (*connect.Response[exportv1.ExportPDFResponse], error) {
	l := h.logger.With(slog.String("method", "ExportActivitiesToPDF"))
	l.InfoContext(ctx, "exporting activities to PDF", slog.Int("count", len(req.Msg.Activities)))

	if len(req.Msg.Activities) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no activities to export"))
	}

	pdfData, err := h.pdfGen.GenerateActivitiesPDF(req.Msg.Activities, req.Msg.Title, req.Msg.CityName)
	if err != nil {
		l.ErrorContext(ctx, "failed to generate PDF", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate PDF: %w", err))
	}

	filename := fmt.Sprintf("activities_%s.pdf", time.Now().Format("2006-01-02"))

	return connect.NewResponse(&exportv1.ExportPDFResponse{
		PdfData:     pdfData,
		Filename:    filename,
		ContentType: "application/pdf",
		SizeBytes:   int64(len(pdfData)),
	}), nil
}

// ExportItineraryToPDF exports an itinerary to PDF
func (h *Handler) ExportItineraryToPDF(
	ctx context.Context,
	req *connect.Request[exportv1.ExportItineraryRequest],
) (*connect.Response[exportv1.ExportPDFResponse], error) {
	l := h.logger.With(slog.String("method", "ExportItineraryToPDF"))
	l.InfoContext(ctx, "exporting itinerary to PDF")

	if req.Msg.Itinerary == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("no itinerary to export"))
	}

	pdfData, err := h.pdfGen.GenerateItineraryPDF(req.Msg.Itinerary)
	if err != nil {
		l.ErrorContext(ctx, "failed to generate PDF", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate PDF: %w", err))
	}

	filename := fmt.Sprintf("itinerary_%s.pdf", time.Now().Format("2006-01-02"))

	return connect.NewResponse(&exportv1.ExportPDFResponse{
		PdfData:     pdfData,
		Filename:    filename,
		ContentType: "application/pdf",
		SizeBytes:   int64(len(pdfData)),
	}), nil
}

// ExportListToPDF exports a list to PDF
func (h *Handler) ExportListToPDF(
	ctx context.Context,
	req *connect.Request[exportv1.ExportListRequest],
) (*connect.Response[exportv1.ExportPDFResponse], error) {
	l := h.logger.With(slog.String("method", "ExportListToPDF"))
	l.InfoContext(ctx, "exporting list to PDF", slog.String("list_id", req.Msg.ListId))

	pdfData, err := h.pdfGen.GenerateListPDF(req.Msg.ListName, req.Msg.Pois, req.Msg.Hotels, req.Msg.Restaurants)
	if err != nil {
		l.ErrorContext(ctx, "failed to generate PDF", slog.Any("error", err))
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to generate PDF: %w", err))
	}

	filename := fmt.Sprintf("list_%s.pdf", time.Now().Format("2006-01-02"))

	return connect.NewResponse(&exportv1.ExportPDFResponse{
		PdfData:     pdfData,
		Filename:    filename,
		ContentType: "application/pdf",
		SizeBytes:   int64(len(pdfData)),
	}), nil
}
