package sse

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/storage"
	"github.com/mochi-mqtt/server/v2/packets"
	"github.com/mochi-mqtt/server/v2/system"
	"gorm.io/gorm"
)

// DB Storage Models for GORM auto-migration
type MQTTClient struct {
	ID   string `gorm:"primaryKey"`
	Data []byte `gorm:"type:bytes"` // Supports varying db constraints for blobs
}

type MQTTSubscription struct {
	ID   string `gorm:"primaryKey"`
	Data []byte `gorm:"type:bytes"`
}

type MQTTRetained struct {
	ID   string `gorm:"primaryKey"`
	Data []byte `gorm:"type:bytes"`
}

type MQTTInflight struct {
	ID   string `gorm:"primaryKey"`
	Data []byte `gorm:"type:bytes"`
}

type MQTTSystemInfo struct {
	ID   string `gorm:"primaryKey"`
	Data []byte `gorm:"type:bytes"`
}

// clientKey returns a primary key for a client.
func clientKey(cl *mqtt.Client) string {
	return storage.ClientKey + "_" + cl.ID
}

// subscriptionKey returns a primary key for a subscription.
func subscriptionKey(cl *mqtt.Client, filter string) string {
	return storage.SubscriptionKey + "_" + cl.ID + ":" + filter
}

// retainedKey returns a primary key for a retained message.
func retainedKey(topic string) string {
	return storage.RetainedKey + "_" + topic
}

// inflightKey returns a primary key for an inflight message.
func inflightKey(cl *mqtt.Client, pk packets.Packet) string {
	return storage.InflightKey + "_" + cl.ID + ":" + pk.FormatID()
}

// sysInfoKey returns a primary key for system info.
func sysInfoKey() string {
	return storage.SysInfoKey
}

// MochiDBHook provides native database persistence for Mochi-MQTT via GORM.
type MochiDBHook struct {
	mqtt.HookBase
	db *gorm.DB
}

// NewMochiDBHook instantiates a new GORM Hook and performs auto-migration.
func NewMochiDBHook(db *gorm.DB) *MochiDBHook {
	db.AutoMigrate(&MQTTClient{}, &MQTTSubscription{}, &MQTTRetained{}, &MQTTInflight{}, &MQTTSystemInfo{})
	return &MochiDBHook{
		db: db,
	}
}

// ID returns the id of the hook.
func (h *MochiDBHook) ID() string {
	return "gorm-db"
}

// Provides indicates which hook methods this hook provides.
func (h *MochiDBHook) Provides(b byte) bool {
	return bytes.Contains([]byte{
		mqtt.OnSessionEstablished,
		mqtt.OnDisconnect,
		mqtt.OnSubscribed,
		mqtt.OnUnsubscribed,
		mqtt.OnRetainMessage,
		mqtt.OnWillSent,
		mqtt.OnQosPublish,
		mqtt.OnQosComplete,
		mqtt.OnQosDropped,
		mqtt.OnSysInfoTick,
		mqtt.OnClientExpired,
		mqtt.OnRetainedExpired,
		mqtt.StoredClients,
		mqtt.StoredInflightMessages,
		mqtt.StoredRetainedMessages,
		mqtt.StoredSubscriptions,
		mqtt.StoredSysInfo,
	}, []byte{b})
}

// Init initializes and auto-migrates the database tables.
func (h *MochiDBHook) Init(config any) error {
	if h.db == nil {
		return errors.New("db connection is required")
	}

	return h.db.AutoMigrate(
		&MQTTClient{},
		&MQTTSubscription{},
		&MQTTRetained{},
		&MQTTInflight{},
		&MQTTSystemInfo{},
	)
}

// OnSessionEstablished adds a client to the store when their session is established.
func (h *MochiDBHook) OnSessionEstablished(cl *mqtt.Client, pk packets.Packet) {
	h.updateClient(cl)
}

// OnWillSent is called when a client sends a Will Message and the Will Message is removed from the client record.
func (h *MochiDBHook) OnWillSent(cl *mqtt.Client, pk packets.Packet) {
	h.updateClient(cl)
}

// updateClient writes the client data to the store.
func (h *MochiDBHook) updateClient(cl *mqtt.Client) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	props := cl.Properties.Props.Copy(false)
	in := &storage.Client{
		ID:              cl.ID,
		T:               storage.ClientKey,
		Remote:          cl.Net.Remote,
		Listener:        cl.Net.Listener,
		Username:        cl.Properties.Username,
		Clean:           cl.Properties.Clean,
		ProtocolVersion: cl.Properties.ProtocolVersion,
		Properties: storage.ClientProperties{
			SessionExpiryInterval: props.SessionExpiryInterval,
			AuthenticationMethod:  props.AuthenticationMethod,
			AuthenticationData:    props.AuthenticationData,
			RequestProblemInfo:    props.RequestProblemInfo,
			RequestResponseInfo:   props.RequestResponseInfo,
			ReceiveMaximum:        props.ReceiveMaximum,
			TopicAliasMaximum:     props.TopicAliasMaximum,
			User:                  props.User,
			MaximumPacketSize:     props.MaximumPacketSize,
		},
		Will: storage.ClientWill(cl.Properties.Will),
	}

	data, err := in.MarshalBinary()
	if err != nil {
		h.Log.Error("failed to serialize client data", "error", err)
		return
	}

	rec := MQTTClient{ID: clientKey(cl), Data: data}
	err = h.db.Save(&rec).Error
	if err != nil {
		h.Log.Error("failed to upsert client data", "error", err, "data", in)
	}
}

// OnDisconnect removes a client from the store if their session has expired.
func (h *MochiDBHook) OnDisconnect(cl *mqtt.Client, _ error, expire bool) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	h.updateClient(cl)

	if !expire {
		return
	}

	if errors.Is(cl.StopCause(), packets.ErrSessionTakenOver) {
		return
	}

	_ = h.db.Delete(&MQTTClient{ID: clientKey(cl)}).Error
}

// OnSubscribed adds one or more client subscriptions to the store.
func (h *MochiDBHook) OnSubscribed(cl *mqtt.Client, pk packets.Packet, reasonCodes []byte) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	for i := 0; i < len(pk.Filters); i++ {
		in := &storage.Subscription{
			ID:                subscriptionKey(cl, pk.Filters[i].Filter),
			T:                 storage.SubscriptionKey,
			Client:            cl.ID,
			Qos:               reasonCodes[i],
			Filter:            pk.Filters[i].Filter,
			Identifier:        pk.Filters[i].Identifier,
			NoLocal:           pk.Filters[i].NoLocal,
			RetainHandling:    pk.Filters[i].RetainHandling,
			RetainAsPublished: pk.Filters[i].RetainAsPublished,
		}

		data, err := in.MarshalBinary()
		if err == nil {
			_ = h.db.Save(&MQTTSubscription{ID: in.ID, Data: data}).Error
		}
	}
}

// OnUnsubscribed removes one or more client subscriptions from the store.
func (h *MochiDBHook) OnUnsubscribed(cl *mqtt.Client, pk packets.Packet) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	for i := 0; i < len(pk.Filters); i++ {
		_ = h.db.Delete(&MQTTSubscription{ID: subscriptionKey(cl, pk.Filters[i].Filter)}).Error
	}
}

// OnRetainMessage adds a retained message for a topic to the store.
// OnRetainMessage adds a retained message for a topic to the store.
func (h *MochiDBHook) OnRetainMessage(cl *mqtt.Client, pk packets.Packet, r int64) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	if r == -1 {
		_ = h.db.Delete(&MQTTRetained{ID: retainedKey(pk.TopicName)}).Error
		return
	}

	props := pk.Properties.Copy(false)
	in := &storage.Message{
		ID:          retainedKey(pk.TopicName),
		T:           storage.RetainedKey,
		FixedHeader: pk.FixedHeader,
		TopicName:   pk.TopicName,
		Payload:     pk.Payload,
		Created:     pk.Created,
		Client:      cl.ID,
		Origin:      pk.Origin,
		Properties: storage.MessageProperties{
			PayloadFormat:          props.PayloadFormat,
			MessageExpiryInterval:  props.MessageExpiryInterval,
			ContentType:            props.ContentType,
			ResponseTopic:          props.ResponseTopic,
			CorrelationData:        props.CorrelationData,
			SubscriptionIdentifier: props.SubscriptionIdentifier,
			TopicAlias:             props.TopicAlias,
			User:                   props.User,
		},
	}

	data, err := in.MarshalBinary()
	if err == nil {
		err = h.db.Save(&MQTTRetained{ID: in.ID, Data: data}).Error
		if err != nil {
			h.Log.Error("failed to save retained message", "topic", pk.TopicName, "error", err)
		}
	}
}

// OnQosPublish adds or updates an inflight message in the store.
func (h *MochiDBHook) OnQosPublish(cl *mqtt.Client, pk packets.Packet, sent int64, resends int) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	props := pk.Properties.Copy(false)
	in := &storage.Message{
		ID:          inflightKey(cl, pk),
		T:           storage.InflightKey,
		Client:      cl.ID,
		Origin:      pk.Origin,
		PacketID:    pk.PacketID,
		FixedHeader: pk.FixedHeader,
		TopicName:   pk.TopicName,
		Payload:     pk.Payload,
		Sent:        sent,
		Created:     pk.Created,
		Properties: storage.MessageProperties{
			PayloadFormat:          props.PayloadFormat,
			MessageExpiryInterval:  props.MessageExpiryInterval,
			ContentType:            props.ContentType,
			ResponseTopic:          props.ResponseTopic,
			CorrelationData:        props.CorrelationData,
			SubscriptionIdentifier: props.SubscriptionIdentifier,
			TopicAlias:             props.TopicAlias,
			User:                   props.User,
		},
	}

	data, err := in.MarshalBinary()
	if err == nil {
		_ = h.db.Save(&MQTTInflight{ID: in.ID, Data: data}).Error
	}
}

// OnQosComplete removes a resolved inflight message from the store.
func (h *MochiDBHook) OnQosComplete(cl *mqtt.Client, pk packets.Packet) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}
	_ = h.db.Delete(&MQTTInflight{ID: inflightKey(cl, pk)}).Error
}

// OnQosDropped removes a dropped inflight message from the store.
func (h *MochiDBHook) OnQosDropped(cl *mqtt.Client, pk packets.Packet) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
	}
	h.OnQosComplete(cl, pk)
}

// OnSysInfoTick stores the latest system info in the store.
func (h *MochiDBHook) OnSysInfoTick(sys *system.Info) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	in := &storage.SystemInfo{
		ID:   sysInfoKey(),
		T:    storage.SysInfoKey,
		Info: *sys.Clone(),
	}

	data, err := in.MarshalBinary()
	if err == nil {
		_ = h.db.Save(&MQTTSystemInfo{ID: in.ID, Data: data}).Error
	}
}

// OnRetainedExpired deletes expired retained messages from the store.
func (h *MochiDBHook) OnRetainedExpired(filter string) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	_ = h.db.Delete(&MQTTRetained{ID: retainedKey(filter)}).Error
}

// OnClientExpired deleted expired clients from the store.
func (h *MochiDBHook) OnClientExpired(cl *mqtt.Client) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	_ = h.db.Delete(&MQTTClient{ID: clientKey(cl)}).Error
}

// StoredClients returns all stored clients from the store.
func (h *MochiDBHook) StoredClients() (v []storage.Client, err error) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	var records []MQTTClient
	if err := h.db.Where("id LIKE ?", storage.ClientKey+"_%").Find(&records).Error; err != nil {
		h.Log.Error("failed to find clients", "error", err)
		return nil, err
	}

	for _, rec := range records {
		obj := storage.Client{}
		if err := obj.UnmarshalBinary(rec.Data); err == nil {
			v = append(v, obj)
		}
	}
	return
}

// StoredSubscriptions returns all stored subscriptions from the store.
func (h *MochiDBHook) StoredSubscriptions() (v []storage.Subscription, err error) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	var records []MQTTSubscription
	if err := h.db.Where("id LIKE ?", storage.SubscriptionKey+"_%").Find(&records).Error; err != nil {
		return nil, err
	}

	for _, rec := range records {
		obj := storage.Subscription{}
		if err := obj.UnmarshalBinary(rec.Data); err == nil {
			v = append(v, obj)
		}
	}
	return
}

// StoredRetainedMessages returns all stored retained messages from the store.
func (h *MochiDBHook) StoredRetainedMessages() (v []storage.Message, err error) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	var records []MQTTRetained
	query := storage.RetainedKey + "_%"
	if err := h.db.Where("id LIKE ?", query).Find(&records).Error; err != nil {
		h.Log.Error("failed to query retained messages", "query", query, "error", err)
		return nil, err
	}

	for _, rec := range records {
		obj := storage.Message{}
		if err := obj.UnmarshalBinary(rec.Data); err == nil {
			v = append(v, obj)
		} else {
			h.Log.Error("failed to unmarshal retained message", "id", rec.ID, "error", err)
		}
	}
	return
}

// StoredInflightMessages returns all stored inflight messages from the store.
func (h *MochiDBHook) StoredInflightMessages() (v []storage.Message, err error) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	var records []MQTTInflight
	if err := h.db.Where("id LIKE ?", storage.InflightKey+"_%").Find(&records).Error; err != nil {
		return nil, err
	}

	for _, rec := range records {
		obj := storage.Message{}
		if err := obj.UnmarshalBinary(rec.Data); err == nil {
			v = append(v, obj)
		}
	}
	return
}

// StoredSysInfo returns the system info from the store.
func (h *MochiDBHook) StoredSysInfo() (v storage.SystemInfo, err error) {
	if h.db == nil {
		h.Log.Error("", "error", storage.ErrDBFileNotOpen)
		return
	}

	var rec MQTTSystemInfo
	if err := h.db.First(&rec, "id = ?", sysInfoKey()).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return v, err
		}
		return v, nil
	}

	err = v.UnmarshalBinary(rec.Data)
	return
}

// Errorf satisfies the badger interface for an error logger.
func (h *MochiDBHook) Errorf(m string, v ...any) {
	h.Log.Error(fmt.Sprintf(strings.ToLower(strings.Trim(m, "\n")), v...), "v", v)
}

// Warningf satisfies the badger interface for a warning logger.
func (h *MochiDBHook) Warningf(m string, v ...any) {
	h.Log.Warn(fmt.Sprintf(strings.ToLower(strings.Trim(m, "\n")), v...), "v", v)
}

// Infof satisfies the badger interface for an info logger.
func (h *MochiDBHook) Infof(m string, v ...any) {
	h.Log.Info(fmt.Sprintf(strings.ToLower(strings.Trim(m, "\n")), v...), "v", v)
}

// Debugf satisfies the badger interface for a debug logger.
func (h *MochiDBHook) Debugf(m string, v ...any) {
	h.Log.Debug(fmt.Sprintf(strings.ToLower(strings.Trim(m, "\n")), v...), "v", v)
}
