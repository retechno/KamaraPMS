package documents

// The Indonesian of the documents of the banquet (36 4.6): the BEO, the proposal and the contract. What people typed (names, the text of a section, the description of a line) is printed as it is.

func init() {
	for k, v := range map[string]string{
		"Banquet proposal": "Proposal banquet", "BANQUET PROPOSAL": "PROPOSAL BANQUET", "Banquet contract": "Kontrak banquet", "BANQUET CONTRACT": "KONTRAK BANQUET",
		"Banquet event order": "Perintah acara banquet (BEO)", "BANQUET EVENT ORDER": "PERINTAH ACARA BANQUET (BEO)",
		"Event": "Acara", "Event number": "Nomor acara", "Client": "Pemesan", "Contact": "Kontak", "Event type": "Jenis acara", "Prepared by": "Disiapkan oleh", "Valid until": "Berlaku sampai",
		"Programme": "Rangkaian acara", "Session": "Sesi", "Time": "Jam", "Room / set-up": "Ruang / susunan", "Items and prices": "Item dan harga", "Item": "Item", "Quantity": "Jumlah",
		"Price": "Harga", "Amount": "Jumlah uang", "Net (before service and tax)": "Neto (sebelum servis dan pajak)", "Service charge": "Biaya layanan", "Tax": "Pajak", "Deposit schedule": "Jadwal deposit",
		"Payment": "Pembayaran", "Due date": "Jatuh tempo", "Percent": "Persen", "Cancellation policy": "Kebijakan pembatalan", "Days before the event": "Hari sebelum acara", "Charge": "Biaya",
		"Terms": "Ketentuan", "Revision": "Revisi", "Revision ": "Revisi ", "Status of the event": "Status acara", "Set-up": "Susunan", "Sales": "Sales", "Items": "Item", "Run of the day": "Susunan acara",
		"Draft": "Draf", "Completed": "Selesai", "Tentative": "Tentatif", "nett": "nett", "Includes: ": "Termasuk: ", "Discount ": "Diskon ",
		"General": "Umum", "Kitchen": "Dapur", "F&B service": "F&B service", "Housekeeping": "Housekeeping", "Engineering": "Engineering", "Security": "Security", "Front office": "Front office",
		"Theater": "Teater", "Classroom": "Kelas", "U-shape": "Bentuk U", "Round table": "Meja bulat", "Cocktail": "Koktail", "Boardroom": "Rapat",
		"Definite": "Definit", //nolint:misspell // the Indonesian word, as the screens say it

		"Name, date and signature":                         "Nama, tanggal dan tanda tangan",
		"This contract is final for revision ":             "Kontrak ini final untuk revisi ",
		" of the event ":                                   " dari acara ",
		" and replaces any earlier contract of the event.": " dan menggantikan kontrak sebelumnya dari acara ini.",
		"A price marked nett includes the service charge and the tax; a price marked ++ is before them. The amounts are worked out at the guaranteed number of guests (the estimated number while none is guaranteed); the bill follows the guests served, never fewer than the guaranteed number.": "Harga bertanda nett sudah termasuk biaya layanan dan pajak; harga bertanda ++ belum termasuk. Jumlah dihitung dengan jumlah tamu yang dijamin (jumlah perkiraan selama belum ada jaminan); tagihan mengikuti tamu yang dilayani, tidak kurang dari jumlah yang dijamin.",
		"The percentages apply to the estimated net revenue of the event (before the service charge and the tax).":                                                               "Persentase berlaku atas perkiraan pendapatan neto acara (sebelum biaya layanan dan pajak).",
		"The percentages apply to the contract total (with the service charge and the tax).":                                                                                     "Persentase berlaku atas total kontrak (dengan biaya layanan dan pajak).",
		"This proposal is an offer, not a booking: the date is held for the client only by the option date of the hotel or by a signed contract.":                                "Proposal ini adalah penawaran, bukan pemesanan: tanggal hanya ditahan untuk pemesan sampai tanggal opsi hotel atau dengan kontrak yang ditandatangani.",
		"The hotel provides the rooms, the items and the services listed for the sessions above, at the prices and on the dates shown.":                                          "Hotel menyediakan ruang, item, dan layanan yang tercantum untuk sesi di atas, dengan harga dan pada tanggal yang tertera.",
		"The client pays the deposits on the dates of the schedule. The hotel may release the rooms if a deposit is not paid on its due date.":                                   "Pemesan membayar deposit pada tanggal dalam jadwal. Hotel boleh melepas ruang bila deposit tidak dibayar pada tanggal jatuh tempo.",
		"The client confirms the number of guests (the guaranteed number) before the first session. The bill follows the guests served, never fewer than the guaranteed number.": "Pemesan memastikan jumlah tamu (jumlah yang dijamin) sebelum sesi pertama. Tagihan mengikuti tamu yang dilayani, tidak kurang dari jumlah yang dijamin.",
		"A change to the dates, the hours, the rooms, the number of guests or the items is a new revision of the event; the hotel issues a new contract for it.":                 "Perubahan tanggal, jam, ruang, jumlah tamu, atau item adalah revisi baru acara; hotel menerbitkan kontrak baru untuknya.",
		"A cancellation is charged by the cancellation policy above, by the number of days before the first session.":                                                            "Pembatalan dikenai biaya menurut kebijakan pembatalan di atas, berdasarkan jumlah hari sebelum sesi pertama.",
	} {
		if _, ok := idExact[k]; !ok {
			idExact[k] = v
		}
	}
	idRules = append(idRules,
		rx(`^(\d+) guaranteed$`, "$1 dijamin"),
		rx(`^(\d+) estimated$`, "$1 perkiraan"),
		rx(`^(\d+) days or more before the first session$`, "$1 hari atau lebih sebelum sesi pertama"),
		rx(`^(\d+) to (\d+) days before the first session$`, "$1 sampai $2 hari sebelum sesi pertama"),
		rx(`^(\S+)% charged as a fee$`, "$1% dikenakan sebagai biaya"),
		rx(`^(\S+)% of the deposit kept$`, "$1% deposit ditahan"),
		rx(`^For the client: (.*)$`, "Untuk pemesan: $1"),
		rx(`^For the hotel: (.*)$`, "Untuk hotel: $1"),
	)
}
