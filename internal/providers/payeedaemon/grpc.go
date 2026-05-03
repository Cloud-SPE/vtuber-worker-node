package payeedaemon

import (
	"context"
	"fmt"
	"math/big"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	paymentsv1 "github.com/Cloud-SPE/vtuber-worker-node/internal/contract/paymentsv1"
)

// grpcClient is the production Client, talking to the daemon over a
// unix socket. Construct via Dial.
type grpcClient struct {
	conn   *grpc.ClientConn
	client paymentsv1.PayeeDaemonClient
}

// Dial connects to the PayeeDaemon at the given unix socket path and
// returns a Client. The caller MUST call Close when done.
//
// The socket path should NOT include the `unix://` prefix — Dial adds
// it internally to keep the transport details out of config.
func Dial(socketPath string) (Client, error) {
	if socketPath == "" {
		return nil, fmt.Errorf("payeedaemon: Dial requires a socket path")
	}
	conn, err := grpc.NewClient("unix://"+socketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("payeedaemon: dial %q: %w", socketPath, err)
	}
	return &grpcClient{
		conn:   conn,
		client: paymentsv1.NewPayeeDaemonClient(conn),
	}, nil
}

func (c *grpcClient) Close() error {
	if c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func (c *grpcClient) OpenSession(ctx context.Context, req OpenSessionRequest) (OpenSessionResult, error) {
	resp, err := c.client.OpenSession(ctx, &paymentsv1.OpenSessionRequest{
		WorkId:              req.WorkID,
		Capability:          req.Capability,
		Offering:            req.Offering,
		PricePerWorkUnitWei: req.PricePerWorkUnitWei.Bytes(),
		WorkUnit:            req.WorkUnit,
	})
	if err != nil {
		return OpenSessionResult{}, fmt.Errorf("payeedaemon: OpenSession: %w", err)
	}
	switch resp.GetOutcome() {
	case paymentsv1.OpenSessionResponse_OUTCOME_OPENED:
		return OpenSessionResult{Outcome: OpenSessionOutcomeOpened}, nil
	case paymentsv1.OpenSessionResponse_OUTCOME_ALREADY_OPEN:
		return OpenSessionResult{Outcome: OpenSessionOutcomeAlreadyOpen}, nil
	default:
		return OpenSessionResult{}, fmt.Errorf("payeedaemon: OpenSession: unexpected outcome %s", resp.GetOutcome().String())
	}
}

func (c *grpcClient) ListCapabilities(ctx context.Context) (ListCapabilitiesResult, error) {
	resp, err := c.client.ListCapabilities(ctx, &paymentsv1.ListCapabilitiesRequest{})
	if err != nil {
		return ListCapabilitiesResult{}, fmt.Errorf("payeedaemon: ListCapabilities: %w", err)
	}
	caps := make([]Capability, 0, len(resp.GetCapabilities()))
	for _, c := range resp.GetCapabilities() {
		offerings := make([]OfferingPrice, 0, len(c.GetOfferings()))
		for _, o := range c.GetOfferings() {
			offerings = append(offerings, OfferingPrice{
				ID:                  o.GetId(),
				PricePerWorkUnitWei: priceInfoToWeiString(o.GetPriceInfo()),
			})
		}
		caps = append(caps, Capability{
			Capability: c.GetCapability(),
			WorkUnit:   c.GetWorkUnit(),
			Offerings:  offerings,
		})
	}
	return ListCapabilitiesResult{
		Capabilities: caps,
	}, nil
}

func (c *grpcClient) ProcessPayment(ctx context.Context, paymentBytes []byte, workID string) (ProcessPaymentResult, error) {
	resp, err := c.client.ProcessPayment(ctx, &paymentsv1.ProcessPaymentRequest{
		PaymentBytes: paymentBytes,
		WorkId:       workID,
	})
	if err != nil {
		return ProcessPaymentResult{}, fmt.Errorf("payeedaemon: ProcessPayment: %w", err)
	}
	return ProcessPaymentResult{
		Sender:        resp.GetSender(),
		CreditedEVWei: new(big.Int).SetBytes(resp.GetCreditedEv()),
		BalanceWei:    new(big.Int).SetBytes(resp.GetBalance()),
		WinnersQueued: resp.GetWinnersQueued(),
	}, nil
}

func (c *grpcClient) GetQuote(ctx context.Context, sender []byte, capability string) (GetQuoteResult, error) {
	resp, err := c.client.GetQuote(ctx, &paymentsv1.GetQuoteRequest{
		Sender:     sender,
		Capability: capability,
	})
	if err != nil {
		return GetQuoteResult{}, fmt.Errorf("payeedaemon: GetQuote: %w", err)
	}
	tp := resp.GetTicketParams()
	out := GetQuoteResult{
		TicketParams: TicketParams{
			Recipient:         tp.GetRecipient(),
			FaceValueWei:      tp.GetFaceValue(),
			WinProb:           tp.GetWinProb(),
			RecipientRandHash: tp.GetRecipientRandHash(),
			Seed:              tp.GetSeed(),
			ExpirationBlock:   tp.GetExpirationBlock(),
			ExpirationParams: TicketExpirationParams{
				CreationRound:          tp.GetExpirationParams().GetCreationRound(),
				CreationRoundBlockHash: tp.GetExpirationParams().GetCreationRoundBlockHash(),
			},
		},
		OfferingPrices: make([]OfferingPrice, 0, len(resp.GetOfferingPrices())),
	}
	for _, o := range resp.GetOfferingPrices() {
		out.OfferingPrices = append(out.OfferingPrices, OfferingPrice{
			ID:                  o.GetId(),
			PricePerWorkUnitWei: priceInfoToWeiString(o.GetPriceInfo()),
		})
	}
	return out, nil
}

func (c *grpcClient) GetTicketParams(ctx context.Context, req GetTicketParamsRequest) (TicketParams, error) {
	resp, err := c.client.GetTicketParams(ctx, &paymentsv1.GetTicketParamsRequest{
		Sender:     append([]byte(nil), req.Sender...),
		Recipient:  append([]byte(nil), req.Recipient...),
		FaceValue:  req.FaceValue.Bytes(),
		Capability: req.Capability,
		Offering:   req.Offering,
	})
	if err != nil {
		return TicketParams{}, fmt.Errorf("payeedaemon: GetTicketParams: %w", err)
	}
	tp := resp.GetTicketParams()
	if tp == nil {
		return TicketParams{}, fmt.Errorf("payeedaemon: GetTicketParams: daemon returned nil ticket_params")
	}
	return TicketParams{
		Recipient:         tp.GetRecipient(),
		FaceValueWei:      tp.GetFaceValue(),
		WinProb:           tp.GetWinProb(),
		RecipientRandHash: tp.GetRecipientRandHash(),
		Seed:              tp.GetSeed(),
		ExpirationBlock:   tp.GetExpirationBlock(),
		ExpirationParams: TicketExpirationParams{
			CreationRound:          tp.GetExpirationParams().GetCreationRound(),
			CreationRoundBlockHash: tp.GetExpirationParams().GetCreationRoundBlockHash(),
		},
	}, nil
}

func (c *grpcClient) DebitBalance(ctx context.Context, sender []byte, workID string, workUnits int64, debitSeq uint64) (DebitBalanceResult, error) {
	req := &paymentsv1.DebitBalanceRequest{
		Sender:    sender,
		WorkId:    workID,
		WorkUnits: workUnits,
		DebitSeq:  debitSeq,
	}
	resp, err := c.client.DebitBalance(ctx, req)
	if err != nil {
		return DebitBalanceResult{}, fmt.Errorf("payeedaemon: DebitBalance: %w", err)
	}
	return DebitBalanceResult{
		BalanceWei: new(big.Int).SetBytes(resp.GetBalance()),
	}, nil
}

func (c *grpcClient) SufficientBalance(ctx context.Context, sender []byte, workID string, minWorkUnits int64) (SufficientBalanceResult, error) {
	resp, err := c.client.SufficientBalance(ctx, &paymentsv1.SufficientBalanceRequest{
		Sender:       sender,
		WorkId:       workID,
		MinWorkUnits: minWorkUnits,
	})
	if err != nil {
		return SufficientBalanceResult{}, fmt.Errorf("payeedaemon: SufficientBalance: %w", err)
	}
	return SufficientBalanceResult{
		Sufficient: resp.GetSufficient(),
		BalanceWei: new(big.Int).SetBytes(resp.GetBalance()),
	}, nil
}

func (c *grpcClient) CloseSession(ctx context.Context, sender []byte, workID string) error {
	if _, err := c.client.CloseSession(ctx, &paymentsv1.PayeeDaemonCloseSessionRequest{
		Sender: sender,
		WorkId: workID,
	}); err != nil {
		return fmt.Errorf("payeedaemon: CloseSession: %w", err)
	}
	return nil
}

// priceInfoToWeiString converts a paymentsv1.PriceInfo (int64
// pricePerUnit / pixelsPerUnit) into a decimal wei string. The daemon
// stores per-unit prices as integer wei values; pixelsPerUnit is a
// scale denominator inherited from the go-livepeer wire format.
//
// For worker-node pricing pixelsPerUnit is always 1, making the result
// just pricePerUnit as a decimal string. The divide is defensive so a
// future scale change doesn't silently wrong-number.
func priceInfoToWeiString(p *paymentsv1.PriceInfo) string {
	if p == nil {
		return "0"
	}
	num := p.GetPricePerUnit()
	den := p.GetPixelsPerUnit()
	if den <= 0 {
		return fmt.Sprintf("%d", num)
	}
	return fmt.Sprintf("%d", num/den)
}
