//go:generate protoc -I . ./state_service.proto --go_out=plugins=grpc:.

package escrow

import (
	"errors"
	"fmt"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"math/big"
)

// PaymentChannelStateService is an implementation of PaymentChannelStateServiceServer gRPC interface
type PaymentChannelStateService struct {
	channelService PaymentChannelService
	paymentStorage *PaymentStorage
}

// verifies whether storage channel nonce is equal to blockchain nonce or not
func (service *PaymentChannelStateService) StorageNonceMatchesWithBlockchainNonce(key *PaymentChannelKey) (equal bool, err error) {
	h := service.channelService

	storageChannel, storageOk, err := h.PaymentChannel(key)
	if err != nil {
		return
	}
	if !storageOk {
		return false, errors.New("unable to read channel details from storage.")
	}

	blockchainChannel, blockchainOk, err := h.PaymentChannelFromBlockChain(key)
	if err != nil {
		return false, errors.New("channel error:" + err.Error())
	}
	if !blockchainOk {
		return false, errors.New("unable to read channel details from blockchain.")
	}

	return storageChannel.Nonce.Cmp(blockchainChannel.Nonce) == 0, nil
}

// NewPaymentChannelStateService returns new instance of PaymentChannelStateService
func NewPaymentChannelStateService(channelService PaymentChannelService, paymentStorage *PaymentStorage) *PaymentChannelStateService {
	return &PaymentChannelStateService{
		channelService: channelService,
		paymentStorage: paymentStorage,
	}
}

// GetChannelState returns the latest state of the channel which id is passed
// in request. To authenticate sender request should also contain correct
// signature of the channel id.
func (service *PaymentChannelStateService) GetChannelState(context context.Context, request *ChannelStateRequest) (reply *ChannelStateReply, err error) {
	log.WithFields(log.Fields{
		"context": context,
		"request": request,
	}).Debug("GetChannelState called")

	channelID := bytesToBigInt(request.GetChannelId())
	signature := request.GetSignature()
	sender, err := getSignerAddressFromMessage(bigIntToBytes(channelID), signature)
	if err != nil {
		return nil, errors.New("incorrect signature")
	}
	channel, ok, err := service.channelService.PaymentChannel(&PaymentChannelKey{ID: channelID})
	if err != nil {
		return nil, errors.New("channel error:" + err.Error())
	}
	if !ok {
		return nil, fmt.Errorf("channel is not found, channelId: %v", channelID)
	}

	if channel.Signer != *sender {
		return nil, errors.New("only channel signer can get latest channel state")
	}

	// check if nonce matches with blockchain or not
	nonceEqual, err := service.StorageNonceMatchesWithBlockchainNonce(&PaymentChannelKey{ID: channelID})
	if err != nil {
		log.WithError(err).Infof("payment data not available in payment storage.")
	} else if !nonceEqual {
		// check for payments in the payment storage with current nonce -1, this will happen  cli has issues in claiming process

		paymentID := PaymentID(channel.ChannelID, (&big.Int{}).Sub(channel.Nonce, big.NewInt(1)))
		payment, ok, err := service.paymentStorage.Get(paymentID)
		if err != nil {
			log.WithError(err).Errorf("unable to extract old payment from storage")
			return nil, err
		}
		if !ok {

			log.Errorf("old payment is not found in storage, nevertheless local channel nonce is not equal to the blockchain one, channel: %v", channelID)
			return nil, errors.New("channel has different nonce in local storage and blockchain")
		}
		return &ChannelStateReply{
			CurrentNonce:         bigIntToBytes(channel.Nonce),
			CurrentSignedAmount:  bigIntToBytes(channel.AuthorizedAmount),
			CurrentSignature:     channel.Signature,
			OldNonceSignedAmount: bigIntToBytes(payment.Amount),
			OldNonceSignature:    payment.Signature,
		}, nil
	}

	if channel.Signature == nil {
		return &ChannelStateReply{
			CurrentNonce: bigIntToBytes(channel.Nonce),
		}, nil
	}

	return &ChannelStateReply{
		CurrentNonce:        bigIntToBytes(channel.Nonce),
		CurrentSignedAmount: bigIntToBytes(channel.AuthorizedAmount),
		CurrentSignature:    channel.Signature,
	}, nil
}
