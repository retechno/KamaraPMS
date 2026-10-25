package documents

import (
	"bytes"
	"strings"
	"testing"

	"kamarapms/internal/banquet"
	"kamarapms/internal/platform/civil"
)

// The PDF of the banquet (docs/architecture/36-banquet.md 4.6): the proposal, the contract and the BEO, in English and in Indonesian.

func tod(s string) *civil.TimeOfDay { t := civil.MustParseTimeOfDay(s); return &t }

func banquetSnapshot() banquet.Snapshot {
	pct := dec("30")
	return banquet.Snapshot{
		Version: banquet.SnapshotVersion,
		Event:   banquet.SnapEvent{Number: "EVT000007", Name: "Wedding Müller – Café", Status: "DEFINITE", EventType: "Wedding", Booker: "Siti Nurhaliza", ContactName: "Siti", ContactPhone: "+62 812", ContactEmail: "siti@example.test", SalesName: "Dewi"},
		Sessions: []banquet.SnapSession{{
			ID: 11, No: 1, Name: "Reception", Date: d("2026-11-07"), Start: tod("12:00"), End: tod("16:00"), Room: "Ballroom", Setup: "ROUND_TABLE", PaxEstimated: 100, PaxGuaranteed: ptrInt(120), Status: "ACTIVE",
			Sections:  map[string]string{banquet.SectionKitchen: "Halal only, no nuts", banquet.SectionSecurity: "Two guards at the door"},
			RunOfShow: []banquet.RunLine{{Seq: 1, At: *tod("12:00"), Text: "Guests arrive"}, {Seq: 2, At: *tod("13:00"), Text: "Lunch"}},
		}, {
			ID: 12, No: 2, Name: "Cancelled party", Date: d("2026-11-08"), Room: "A", Status: "CANCELLED",
		}},
		Lines: []banquet.Line{
			{ID: 1, SessionID: 11, LineNo: 1, Description: "Fullday Meeting", Basis: "PER_PAX", Quantity: dec("120"), UnitPrice: dec("363000"), PriceMode: banquet.PriceInclusive},
			{ID: 2, SessionID: 11, LineNo: 2, Description: "Flower arrangement", Basis: "PER_EVENT", Quantity: dec("1"), UnitPrice: dec("2500000"), PriceMode: banquet.PriceExclusive},
		},
		Estimate: banquet.Estimate{Net: dec("46000000"), Service: dec("4600000"), Tax: dec("5060000"), Total: dec("55660000")},
		Deposits: []banquet.SnapDeposit{{Label: "Down payment", DueDate: d("2026-10-01"), Percent: &pct, Amount: dec("16698000")}, {Label: "Balance", DueDate: d("2026-11-01"), Amount: dec("38962000")}},
		Policy:   banquet.SnapPolicy{Base: "NET", Tiers: []banquet.Tier{{DaysFrom: 30, Percent: dec("10"), Action: "FEE"}, {DaysFrom: 0, DaysTo: ptrInt(29), Percent: dec("100"), Action: "FORFEIT"}}},
	}
}

func ptrInt(n int) *int { return &n }

func banquetDoc(lang Lang) BanquetDoc {
	return BanquetDoc{Hotel: hotel, Lang: lang, Printed: "Printed 25 Oct 2026 10:00", Decimals: 0, Snap: banquetSnapshot()}
}

func TestBanquetProposalInEnglishAndIndonesian(t *testing.T) {
	en, err := RenderBanquetProposal(banquetDoc(LangEN))
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, en)
	for _, want := range []string{"BANQUET PROPOSAL", "EVT000007", "Hotel Bali", "Reception", "Ballroom", "Fullday Meeting", "Flower arrangement", "nett", "++", "DEPOSIT SCHEDULE", "Down payment", "CANCELLATION POLICY", "Halal"} {
		if want == "Halal" { // the text of a department belongs to the BEO, not to the proposal
			if strings.Contains(s, want) {
				t.Errorf("the proposal prints the internal text of the kitchen")
			}
			continue
		}
		if !strings.Contains(s, want) {
			t.Errorf("proposal lacks %q", want)
		}
	}
	if strings.Contains(s, "Cancelled party") {
		t.Error("a cancelled session is not offered")
	}
	id, err := RenderBanquetProposal(banquetDoc(LangID))
	if err != nil {
		t.Fatal(err)
	}
	s = text(t, id)
	for _, want := range []string{"PROPOSAL BANQUET", "JADWAL DEPOSIT", "KEBIJAKAN PEMBATALAN", "EVT000007", "Down payment"} {
		if !strings.Contains(s, want) {
			t.Errorf("Indonesian proposal lacks %q", want)
		}
	}
	if strings.Contains(s, "CANCELLATION POLICY") || strings.Contains(s, "BANQUET PROPOSAL") {
		t.Error("the Indonesian proposal still has English headings")
	}
}

func TestBanquetContractCarriesItsNumberRevisionAndSignature(t *testing.T) {
	d := banquetDoc(LangEN)
	d.Number, d.Revision = "BQC-000012", 3
	b, err := RenderBanquetContract(d)
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, b)
	for _, want := range []string{"BANQUET CONTRACT", "BQC-000012", "Revision", "EVT000007", "TERMS", "Name, date and signature"} {
		if !strings.Contains(s, want) {
			t.Errorf("contract lacks %q", want)
		}
	}
	d.Lang = LangID
	b, err = RenderBanquetContract(d)
	if err != nil {
		t.Fatal(err)
	}
	s = text(t, b)
	for _, want := range []string{"KONTRAK BANQUET", "BQC-000012", "Revisi", "KETENTUAN", "tanda tangan"} {
		if !strings.Contains(s, want) {
			t.Errorf("Indonesian contract lacks %q", want)
		}
	}
	for _, English := range []string{"The hotel provides", "The client pays", "replaces any earlier contract"} {
		if strings.Contains(s, English) {
			t.Errorf("the Indonesian contract still has the English text %q", English)
		}
	}
}

func TestBeoPrintsEachSectionAndTheRunOfTheDay(t *testing.T) {
	d := banquetDoc(LangEN)
	d.Revision = 4
	b, err := RenderBeo(d)
	if err != nil {
		t.Fatal(err)
	}
	s := text(t, b)
	for _, want := range []string{"BANQUET EVENT ORDER", "EVT000007", "Revision 4", "Reception", "Halal only, no nuts", "Two guards at the door", "Guests arrive", "Lunch", "KITCHEN", "SECURITY"} {
		if !strings.Contains(s, want) {
			t.Errorf("BEO lacks %q", want)
		}
	}
	if strings.Contains(s, "Cancelled party") {
		t.Error("a cancelled session is not on the BEO")
	}
	// the BEO has no price in it: the kitchen does not need them
	if strings.Contains(s, "363000") || strings.Contains(s, "55660000") {
		t.Error("the BEO prints prices")
	}
	if !strings.Contains(s, "Room") || strings.Contains(s, "Kamar") {
		t.Error("the English BEO calls the room of the event Room")
	}
	d.Lang, d.SessionID = LangID, 11
	b, err = RenderBeo(d)
	if err != nil {
		t.Fatal(err)
	}
	s = text(t, b)
	for _, want := range []string{"PERINTAH ACARA BANQUET", "DAPUR", "SUSUNAN ACARA", "Halal only, no nuts", "Ruang"} {
		if !strings.Contains(s, want) {
			t.Errorf("Indonesian BEO lacks %q", want)
		}
	}
	if strings.Contains(s, "Kamar") {
		t.Error("the Indonesian BEO calls the room of the event Kamar, the guest room")
	}
	// a session that is not in the event is not printed
	d.SessionID = 999
	if b, err = RenderBeo(d); err != nil {
		t.Fatal(err)
	} else if strings.Contains(text(t, b), "Halal") {
		t.Error("a BEO of another session printed this one")
	}
}

func TestBanquetDocumentsAreTheSameBytesEveryTime(t *testing.T) {
	a, err := RenderBanquetProposal(banquetDoc(LangEN))
	if err != nil {
		t.Fatal(err)
	}
	b, err := RenderBanquetProposal(banquetDoc(LangEN))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Error("two renderings of the same snapshot differ")
	}
}

func TestADraftBeoSaysSo(t *testing.T) {
	d := banquetDoc(LangEN)
	d.Snap.Event.Status = "TENTATIVE"
	b, err := RenderBeo(d)
	if err != nil {
		t.Fatal(err)
	}
	if s := text(t, b); !strings.Contains(s, "Draft") {
		t.Error("a BEO that has no revision is a draft and says so")
	}
}
