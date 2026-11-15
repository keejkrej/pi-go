package chord

import (
	"slices"
	"strings"
	"testing"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

func tswMustJson(t *testing.T, v any) string {
	t.Helper()
	b, err := jsonx.Marshal(v)
	if err != nil {
		t.Fatalf("jsonx.Marshal failed: %v", err)
	}
	return string(b)
}

func tswParse(t *testing.T, raw string) any {
	t.Helper()
	v, err := jsonx.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("jsonx.Parse failed: %v", err)
	}
	return v
}

func TestServiceWireControlCallsAndValidation(t *testing.T) {
	catCall := CreateServiceCatalogueCall()
	ctrl := DecodeServiceControlCall(catCall)
	if ctrl != (ServiceControlCallCatalogue{Type: "catalogue"}) {
		t.Fatalf("unexpected catalogue control call: %#v", ctrl)
	}

	catInput := tswParse(t, `[{"serviceId":"pi.models","mode":"singleton"},{"serviceId":"pi.dialogs","mode":"keyed"}]`)
	catalogue, err := ParseServiceCatalogue(catInput)
	if err != nil {
		t.Fatalf("ParseServiceCatalogue failed: %v", err)
	}
	wantCatalogue := []ServiceCatalogueEntry{
		{ServiceId: "pi.models", Mode: ServiceModeSingleton},
		{ServiceId: "pi.dialogs", Mode: ServiceModeKeyed},
	}
	if !slices.Equal(catalogue, wantCatalogue) {
		t.Fatalf("expected catalogue %v, got %v", wantCatalogue, catalogue)
	}

	subCall := CreateServiceSubscribeCall("subscription-1", "pi.models", ServiceModeSingleton)
	if got := DecodeServiceControlCall(subCall); got != (ServiceControlCallSubscribe{
		Type:           "subscribe",
		SubscriptionId: "subscription-1",
		ServiceId:      "pi.models",
		Mode:           ServiceModeSingleton,
	}) {
		t.Fatalf("unexpected subscribe control call: %#v", got)
	}

	unsubCall := CreateServiceUnsubscribeCall("subscription-1")
	if got := DecodeServiceControlCall(unsubCall); got != (ServiceControlCallUnsubscribe{
		Type:           "unsubscribe",
		SubscriptionId: "subscription-1",
	}) {
		t.Fatalf("unexpected unsubscribe control call: %#v", got)
	}

	parsedCall, err := ParseServiceCall(tswParse(t, `{
		"serviceId": "pi.question-dialog",
		"instance": {"key": "invocation-1", "generation": 2},
		"member": "submit",
		"args": [{"outcome": "selected", "index": 0}]
	}`))
	if err != nil {
		t.Fatalf("ParseServiceCall failed: %v", err)
	}
	if parsedCall.Member != "submit" || parsedCall.Instance == nil || parsedCall.Instance.Key != "invocation-1" || parsedCall.Instance.Generation != 2 {
		t.Fatalf("unexpected parsed service call: %#v", parsedCall)
	}
}

func TestServiceWireRejectsMalformedValues(t *testing.T) {
	if _, err := ParseServiceCall(tswParse(t, `{"serviceId":"pi.models","member":"list","args":[],"extra":true}`)); err == nil || !strings.Contains(err.Error(), "Invalid service call") {
		t.Fatalf("expected Invalid service call, got %v", err)
	}
	if _, err := ParseServiceCatalogue(tswParse(t, `[{"serviceId":"pi.models","mode":"unknown"}]`)); err == nil || !strings.Contains(err.Error(), "Invalid service catalogue") {
		t.Fatalf("expected Invalid service catalogue, got %v", err)
	}
	if _, err := ParseServiceProviderUpdate(tswParse(t, `{"type":"state","member":"state","sequence":0,"ops":[]}`)); err == nil || !strings.Contains(err.Error(), "Invalid service state update") {
		t.Fatalf("expected Invalid service state update, got %v", err)
	}
	if _, err := ParseWireServiceProviderUpdate(tswParse(t, `{"type":"state","member":"state","sequence":1,"ops":[["?",0]]}`)); err == nil {
		t.Fatal("expected invalid wire op rejection")
	}
}

func TestServiceWireValidatesDecodedAndWireSnapshotsAndUpdates(t *testing.T) {
	snapshot := ServiceSubscriptionSnapshot{
		ServiceId: "pi.models",
		Mode:      ServiceModeSingleton,
		Instances: []ServiceInstanceSnapshot{
			{
				Members: ServiceMemberSnapshotList{
					ServiceMemberSnapshotState{
						Name:     "state",
						Kind:     "state",
						Sequence: 0,
						Ops:      OpList{OpReplace{Value: jsonx.ObjectOf("revision", float64(1))}},
					},
				},
			},
		},
	}
	if _, err := ParseServiceSubscriptionSnapshot(snapshot); err != nil {
		t.Fatalf("ParseServiceSubscriptionSnapshot failed: %v", err)
	}
	encoder := CreateServiceStateEncoder()
	wireSnapshot, err := encoder.EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatalf("EncodeSnapshot failed: %v", err)
	}
	if _, err := ParseWireServiceSubscriptionSnapshot(wireSnapshot); err != nil {
		t.Fatalf("ParseWireServiceSubscriptionSnapshot failed: %v", err)
	}

	update := ServiceProviderUpdateState{
		Type:     "state",
		Member:   "state",
		Sequence: 1,
		Ops:      OpList{OpSet{Path: Path{SegString("revision")}, Value: float64(2)}},
	}
	if _, err := ParseServiceProviderUpdate(update); err != nil {
		t.Fatalf("ParseServiceProviderUpdate failed: %v", err)
	}
	wireUpdate, err := encoder.EncodeUpdate(update)
	if err != nil {
		t.Fatalf("EncodeUpdate failed: %v", err)
	}
	if _, err := ParseWireServiceProviderUpdate(wireUpdate); err != nil {
		t.Fatalf("ParseWireServiceProviderUpdate failed: %v", err)
	}

	// JSON round-trip with preserved unknown variants
	unknownMember := UnknownServiceMemberSnapshot{Raw: jsonx.ObjectOf("kind", "future", "name", "x", "payload", float64(7))}
	mb, err := jsonx.Marshal(ServiceMemberSnapshotList{unknownMember})
	if err != nil {
		t.Fatalf("Marshal ServiceMemberSnapshotList failed: %v", err)
	}
	var decodedMembers ServiceMemberSnapshotList
	if err := jsonx.Unmarshal(mb, &decodedMembers); err != nil {
		t.Fatalf("Unmarshal ServiceMemberSnapshotList failed: %v", err)
	}
	if tswMustJson(t, decodedMembers) != string(mb) {
		t.Fatalf("expected unknown member round-trip %s, got %s", mb, tswMustJson(t, decodedMembers))
	}

	unknownUpdateBytes := []byte(`{"type":"futureEvent","count":3}`)
	u, err := UnmarshalServiceProviderUpdate(unknownUpdateBytes)
	if err != nil {
		t.Fatalf("UnmarshalServiceProviderUpdate failed: %v", err)
	}
	if _, ok := u.(UnknownServiceProviderUpdate); !ok {
		t.Fatalf("expected UnknownServiceProviderUpdate, got %T", u)
	}
	if tswMustJson(t, u) != string(unknownUpdateBytes) {
		t.Fatalf("expected unknown update round-trip %s, got %s", unknownUpdateBytes, tswMustJson(t, u))
	}
}

func TestServiceWireCodecPairPerSubscriptionState(t *testing.T) {
	enc := CreateServiceStateEncoder()
	dec := CreateServiceStateDecoder()
	snapshot := ServiceSubscriptionSnapshot{
		ServiceId: "pi.models",
		Mode:      ServiceModeSingleton,
		Instances: []ServiceInstanceSnapshot{
			{
				Members: ServiceMemberSnapshotList{
					ServiceMemberSnapshotState{
						Name:     "state",
						Kind:     "state",
						Sequence: 0,
						Ops:      OpList{OpReplace{Value: jsonx.ObjectOf("revision", float64(0))}},
					},
				},
			},
		},
	}
	wireSnap, err := enc.EncodeSnapshot(snapshot)
	if err != nil {
		t.Fatalf("EncodeSnapshot failed: %v", err)
	}
	decodedSnap, err := dec.DecodeSnapshot(wireSnap)
	if err != nil {
		t.Fatalf("DecodeSnapshot failed: %v", err)
	}
	if tswMustJson(t, decodedSnap) != tswMustJson(t, snapshot) {
		t.Fatalf("snapshot mismatch:\n got: %s\nwant: %s", tswMustJson(t, decodedSnap), tswMustJson(t, snapshot))
	}

	first := ServiceProviderUpdateState{
		Type:     "state",
		Member:   "state",
		Sequence: 1,
		Ops:      OpList{OpSet{Path: Path{SegString("revision")}, Value: float64(1)}},
	}
	second := ServiceProviderUpdateState{
		Type:     "state",
		Member:   "state",
		Sequence: 2,
		Ops:      OpList{OpSet{Path: Path{SegString("revision")}, Value: float64(2)}},
	}
	firstWire, err := enc.EncodeUpdate(first)
	if err != nil {
		t.Fatalf("EncodeUpdate first failed: %v", err)
	}
	secondWire, err := enc.EncodeUpdate(second)
	if err != nil {
		t.Fatalf("EncodeUpdate second failed: %v", err)
	}
	if got := tswMustJson(t, firstWire); got != `{"type":"state","member":"state","sequence":1,"ops":[["s",["revision"],1]]}` {
		t.Fatalf("unexpected firstWire: %s", got)
	}
	if got := tswMustJson(t, secondWire); got != `{"type":"state","member":"state","sequence":2,"ops":[["#",0,["revision"]],["s",0,2]]}` {
		t.Fatalf("unexpected secondWire: %s", got)
	}
	decFirst, err := dec.DecodeUpdate(firstWire)
	if err != nil {
		t.Fatalf("DecodeUpdate first failed: %v", err)
	}
	decSecond, err := dec.DecodeUpdate(secondWire)
	if err != nil {
		t.Fatalf("DecodeUpdate second failed: %v", err)
	}
	if tswMustJson(t, decFirst) != tswMustJson(t, first) || tswMustJson(t, decSecond) != tswMustJson(t, second) {
		t.Fatal("decoded updates do not match originals")
	}
}

func TestServiceWireValidatesResetsAndRestartsDictionaries(t *testing.T) {
	enc := CreateServiceStateEncoder()
	dec := CreateServiceStateDecoder()
	snapshot := ServiceSubscriptionSnapshot{
		ServiceId: "pi.states",
		Mode:      ServiceModeSingleton,
		Instances: []ServiceInstanceSnapshot{
			{
				Members: ServiceMemberSnapshotList{
					ServiceMemberSnapshotState{
						Name:     "state",
						Kind:     "state",
						Sequence: 0,
						Ops:      OpList{OpReplace{Value: jsonx.ObjectOf("before", float64(0))}},
					},
				},
			},
		},
	}
	ws, _ := enc.EncodeSnapshot(snapshot)
	_, _ = dec.DecodeSnapshot(ws)
	for seq := 1; seq <= 2; seq++ {
		wu, _ := enc.EncodeUpdate(ServiceProviderUpdateState{
			Type:     "state",
			Member:   "state",
			Sequence: seq,
			Ops:      OpList{OpSet{Path: Path{SegString("before")}, Value: float64(seq)}},
		})
		if _, err := dec.DecodeUpdate(wu); err != nil {
			t.Fatalf("DecodeUpdate seq %d failed: %v", seq, err)
		}
	}

	reset := ServiceProviderUpdateReset{
		Type: "reset",
		Snapshot: ServiceSubscriptionSnapshot{
			ServiceId: "pi.states",
			Mode:      ServiceModeSingleton,
			Instances: []ServiceInstanceSnapshot{
				{
					Members: ServiceMemberSnapshotList{
						ServiceMemberSnapshotState{
							Name:     "state",
							Kind:     "state",
							Sequence: 103,
							Ops:      OpList{OpReplace{Value: jsonx.ObjectOf("after", float64(103))}},
						},
					},
				},
			},
		},
	}
	if _, err := ParseServiceProviderUpdate(reset); err != nil {
		t.Fatalf("ParseServiceProviderUpdate(reset) failed: %v", err)
	}
	wireReset, err := enc.EncodeUpdate(reset)
	if err != nil {
		t.Fatalf("EncodeUpdate(reset) failed: %v", err)
	}
	if _, err := ParseWireServiceProviderUpdate(wireReset); err != nil {
		t.Fatalf("ParseWireServiceProviderUpdate(wireReset) failed: %v", err)
	}
	decReset, err := dec.DecodeUpdate(wireReset)
	if err != nil {
		t.Fatalf("DecodeUpdate(wireReset) failed: %v", err)
	}
	if tswMustJson(t, decReset) != tswMustJson(t, reset) {
		t.Fatalf("reset mismatch: %s vs %s", tswMustJson(t, decReset), tswMustJson(t, reset))
	}

	for seq := 104; seq <= 106; seq++ {
		upd := ServiceProviderUpdateState{
			Type:     "state",
			Member:   "state",
			Sequence: seq,
			Ops:      OpList{OpSet{Path: Path{SegString("after")}, Value: float64(seq)}},
		}
		wu, err := enc.EncodeUpdate(upd)
		if err != nil {
			t.Fatalf("EncodeUpdate seq %d failed: %v", seq, err)
		}
		du, err := dec.DecodeUpdate(wu)
		if err != nil {
			t.Fatalf("DecodeUpdate seq %d failed: %v", seq, err)
		}
		if tswMustJson(t, du) != tswMustJson(t, upd) {
			t.Fatalf("seq %d mismatch: %s vs %s", seq, tswMustJson(t, du), tswMustJson(t, upd))
		}
	}

	badResetExtra := tswParse(t, `{"type":"reset","snapshot":{"serviceId":"pi.states","mode":"singleton","instances":[]},"extra":true}`)
	badResetEmpty := tswParse(t, `{"type":"reset","snapshot":{}}`)
	badResetNonRoot := tswParse(t, `{"type":"reset","snapshot":{"serviceId":"pi.states","mode":"singleton","instances":[{"members":[{"name":"state","kind":"state","sequence":103,"ops":[["s",["before"],103]]}]}]}}`)

	if _, err := ParseServiceProviderUpdate(badResetExtra); err == nil {
		t.Fatal("expected ParseServiceProviderUpdate to reject extra key")
	}
	if _, err := ParseWireServiceProviderUpdate(badResetExtra); err == nil {
		t.Fatal("expected ParseWireServiceProviderUpdate to reject extra key")
	}
	if _, err := ParseServiceProviderUpdate(badResetEmpty); err == nil {
		t.Fatal("expected ParseServiceProviderUpdate to reject empty snapshot")
	}
	if _, err := ParseWireServiceProviderUpdate(badResetEmpty); err == nil {
		t.Fatal("expected ParseWireServiceProviderUpdate to reject empty snapshot")
	}
	if _, err := ParseServiceProviderUpdate(badResetNonRoot); err == nil || !strings.Contains(err.Error(), "full root replacements") {
		t.Fatalf("expected full root replacements error, got %v", err)
	}
	if _, err := ParseWireServiceProviderUpdate(badResetNonRoot); err == nil || !strings.Contains(err.Error(), "full root replacements") {
		t.Fatalf("expected full root replacements error, got %v", err)
	}
}

func TestServiceWireIsolatesDictionariesBetweenStatesAndSubscriptions(t *testing.T) {
	snapshot := ServiceSubscriptionSnapshot{
		ServiceId: "pi.states",
		Mode:      ServiceModeSingleton,
		Instances: []ServiceInstanceSnapshot{
			{
				Members: ServiceMemberSnapshotList{
					ServiceMemberSnapshotState{Name: "left", Kind: "state", Sequence: 0, Ops: OpList{OpReplace{Value: jsonx.ObjectOf("revision", float64(0))}}},
					ServiceMemberSnapshotState{Name: "right", Kind: "state", Sequence: 0, Ops: OpList{OpReplace{Value: jsonx.ObjectOf("revision", float64(0))}}},
				},
			},
		},
	}
	enc1, dec1 := CreateServiceStateEncoder(), CreateServiceStateDecoder()
	enc2, dec2 := CreateServiceStateEncoder(), CreateServiceStateDecoder()
	ws1, _ := enc1.EncodeSnapshot(snapshot)
	_, _ = dec1.DecodeSnapshot(ws1)
	ws2, _ := enc2.EncodeSnapshot(snapshot)
	_, _ = dec2.DecodeSnapshot(ws2)

	makeUpd := func(member string, seq, rev int) ServiceProviderUpdateState {
		return ServiceProviderUpdateState{
			Type:     "state",
			Member:   member,
			Sequence: seq,
			Ops:      OpList{OpSet{Path: Path{SegString("revision")}, Value: float64(rev)}},
		}
	}

	firstLeft, _ := enc1.EncodeUpdate(makeUpd("left", 1, 1))
	firstRight, _ := enc1.EncodeUpdate(makeUpd("right", 1, 1))
	secondLeft, _ := enc1.EncodeUpdate(makeUpd("left", 2, 2))
	secondRight, _ := enc1.EncodeUpdate(makeUpd("right", 2, 2))

	if got := tswMustJson(t, firstLeft.(WireServiceProviderUpdateState).Ops); got != `[["s",["revision"],1]]` {
		t.Fatalf("unexpected firstLeft ops: %s", got)
	}
	if got := tswMustJson(t, firstRight.(WireServiceProviderUpdateState).Ops); got != `[["s",["revision"],1]]` {
		t.Fatalf("unexpected firstRight ops: %s", got)
	}
	if got := tswMustJson(t, secondLeft.(WireServiceProviderUpdateState).Ops); got != `[["#",0,["revision"]],["s",0,2]]` {
		t.Fatalf("unexpected secondLeft ops: %s", got)
	}
	if got := tswMustJson(t, secondRight.(WireServiceProviderUpdateState).Ops); got != `[["#",0,["revision"]],["s",0,2]]` {
		t.Fatalf("unexpected secondRight ops: %s", got)
	}

	for _, pair := range []struct {
		w WireServiceProviderUpdate
		u ServiceProviderUpdateState
	}{
		{firstLeft, makeUpd("left", 1, 1)},
		{firstRight, makeUpd("right", 1, 1)},
		{secondLeft, makeUpd("left", 2, 2)},
		{secondRight, makeUpd("right", 2, 2)},
	} {
		du, err := dec1.DecodeUpdate(pair.w)
		if err != nil || tswMustJson(t, du) != tswMustJson(t, pair.u) {
			t.Fatalf("decode mismatch: err=%v got=%s want=%s", err, tswMustJson(t, du), tswMustJson(t, pair.u))
		}
	}

	indepLeft, _ := enc2.EncodeUpdate(makeUpd("left", 1, 1))
	if got := tswMustJson(t, indepLeft.(WireServiceProviderUpdateState).Ops); got != `[["s",["revision"],1]]` {
		t.Fatalf("expected enc2 independent left, got %s", got)
	}
	if du, err := dec2.DecodeUpdate(indepLeft); err != nil || tswMustJson(t, du) != tswMustJson(t, makeUpd("left", 1, 1)) {
		t.Fatalf("dec2 mismatch: %v", err)
	}

	leftBase := ServiceProviderUpdateState{
		Type:     "state",
		Member:   "left",
		Sequence: 3,
		Ops:      OpList{OpReplace{Value: jsonx.ObjectOf("revision", float64(3))}},
	}
	wLeftBase, _ := enc1.EncodeUpdate(leftBase)
	if dLeftBase, err := dec1.DecodeUpdate(wLeftBase); err != nil || tswMustJson(t, dLeftBase) != tswMustJson(t, leftBase) {
		t.Fatalf("leftBase round-trip failed: %v", err)
	}
	thirdRight, _ := enc1.EncodeUpdate(makeUpd("right", 3, 3))
	if got := tswMustJson(t, thirdRight.(WireServiceProviderUpdateState).Ops); got != `[["s",0,3]]` {
		t.Fatalf("expected right dict untouched ([[\"s\",0,3]]), got %s", got)
	}
	if dThirdRight, err := dec1.DecodeUpdate(thirdRight); err != nil || tswMustJson(t, dThirdRight) != tswMustJson(t, makeUpd("right", 3, 3)) {
		t.Fatalf("thirdRight round-trip failed: %v", err)
	}
}

func TestServiceWireKeyedCodecLifecycle(t *testing.T) {
	enc := CreateServiceStateEncoder()
	dec := CreateServiceStateDecoder()
	snapshot := ServiceSubscriptionSnapshot{
		ServiceId: "pi.dialogs",
		Mode:      ServiceModeKeyed,
		Instances: []ServiceInstanceSnapshot{},
	}
	ws, _ := enc.EncodeSnapshot(snapshot)
	ds, _ := dec.DecodeSnapshot(ws)
	if tswMustJson(t, ds) != tswMustJson(t, snapshot) {
		t.Fatalf("keyed snapshot mismatch: %s vs %s", tswMustJson(t, ds), tswMustJson(t, snapshot))
	}

	addr := ServiceInstanceAddress{Key: "dialog-1", Generation: 1}
	spawned := ServiceProviderUpdateSpawned{
		Type: "spawned",
		Instance: ServiceInstanceSnapshot{
			Instance: &addr,
			Members: ServiceMemberSnapshotList{
				ServiceMemberSnapshotState{Name: "request", Kind: "state", Sequence: 0, Ops: OpList{OpReplace{Value: jsonx.ObjectOf("value", float64(0))}}},
			},
		},
	}
	wSpawned, err := enc.EncodeUpdate(spawned)
	if err != nil {
		t.Fatalf("EncodeUpdate spawned failed: %v", err)
	}
	dSpawned, err := dec.DecodeUpdate(wSpawned)
	if err != nil || tswMustJson(t, dSpawned) != tswMustJson(t, spawned) {
		t.Fatalf("spawned round-trip failed: %v", err)
	}

	upd := ServiceProviderUpdateState{
		Type:     "state",
		Instance: &addr,
		Member:   "request",
		Sequence: 1,
		Ops:      OpList{OpSet{Path: Path{SegString("value")}, Value: float64(1)}},
	}
	wUpd, err := enc.EncodeUpdate(upd)
	if err != nil {
		t.Fatalf("EncodeUpdate state failed: %v", err)
	}
	dUpd, err := dec.DecodeUpdate(wUpd)
	if err != nil || tswMustJson(t, dUpd) != tswMustJson(t, upd) {
		t.Fatalf("keyed state update round-trip failed: %v", err)
	}

	closed := ServiceProviderUpdateClosed{Type: "closed", Instance: addr}
	wClosed, err := enc.EncodeUpdate(closed)
	if err != nil {
		t.Fatalf("EncodeUpdate closed failed: %v", err)
	}
	dClosed, err := dec.DecodeUpdate(wClosed)
	if err != nil || tswMustJson(t, dClosed) != tswMustJson(t, closed) {
		t.Fatalf("closed round-trip failed: %v", err)
	}

	upd2 := upd
	upd2.Sequence = 2
	if _, err := enc.EncodeUpdate(upd2); err == nil || !strings.Contains(err.Error(), "Unknown service state") {
		t.Fatalf("expected Unknown service state after close, got %v", err)
	}
}

func TestRemoteServiceEndpointPublishAndCleanup(t *testing.T) {
	type counter struct{}
	counterSvc, err := DefineService[counter]("test.counter", nil)
	if err != nil {
		t.Fatalf("DefineService failed: %v", err)
	}
	provider, err := NewRemoteServiceProvider([]RemoteServiceProviderEntry{{Id: counterSvc.Id}})
	if err != nil {
		t.Fatalf("NewRemoteServiceProvider failed: %v", err)
	}
	state, err := NewReplicatedState[any](jsonx.ObjectOf("value", float64(0)))
	if err != nil {
		t.Fatalf("NewReplicatedState failed: %v", err)
	}
	if err := provider.Provide(counterSvc, jsonx.ObjectOf("state", state)); err != nil {
		t.Fatalf("Provide failed: %v", err)
	}
	endpoint := CreateRemoteServiceEndpoint(provider)
	var updates []ServiceProviderUpdate
	publish := func(_ string, upd ServiceProviderUpdate, _ Context) error {
		updates = append(updates, upd)
		return nil
	}

	catVal, ok, err := endpoint.Invoke(CreateServiceCatalogueCall(), publish, BackgroundContext)
	if err != nil || !ok {
		t.Fatalf("catalogue invoke failed: ok=%v err=%v", ok, err)
	}
	cat, okCat := catVal.([]ServiceCatalogueEntry)
	if !okCat || len(cat) != 1 || cat[0] != (ServiceCatalogueEntry{ServiceId: counterSvc.Id, Mode: ServiceModeSingleton}) {
		t.Fatalf("unexpected catalogue: %#v", catVal)
	}

	subSnapVal, ok, err := endpoint.Invoke(
		CreateServiceSubscribeCall("subscription-1", counterSvc.Id, ServiceModeSingleton),
		publish,
		BackgroundContext,
	)
	if err != nil || !ok {
		t.Fatalf("subscribe invoke failed: ok=%v err=%v", ok, err)
	}
	subSnap := subSnapVal.(ServiceSubscriptionSnapshot)
	if subSnap.ServiceId != counterSvc.Id || subSnap.Mode != ServiceModeSingleton {
		t.Fatalf("unexpected subscribe snapshot: %#v", subSnap)
	}

	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		draft.Value.(*jsonx.Object).Set("value", float64(1))
		return nil
	}); err != nil {
		t.Fatalf("Change 1 failed: %v", err)
	}
	if len(updates) != 1 || tswMustJson(t, updates[0]) != `{"type":"state","member":"state","sequence":1,"ops":[["s",["value"],1]]}` {
		t.Fatalf("unexpected updates after change 1: %v", updates)
	}

	endpoint.Dispose()
	if err := state.Change(BackgroundContext, func(draft Draft[any]) error {
		draft.Value.(*jsonx.Object).Set("value", float64(2))
		return nil
	}); err != nil {
		t.Fatalf("Change 2 failed: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("expected no further updates after dispose, got %d", len(updates))
	}
	if err := provider.Dispose(); err != nil {
		t.Fatalf("provider.Dispose failed: %v", err)
	}
}
