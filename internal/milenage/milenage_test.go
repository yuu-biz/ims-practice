package milenage

import (
	"encoding/hex"
	"testing"
)

// testSet is one MILENAGE conformance test set of TS 35.208 (Release
// 19) 4.3, in the hexadecimal format of the specification. Sets 1 and
// 2 are the same data, as TS 35.208 4.1 explains.
type testSet struct {
	set                        int
	K, RAND, SQN, AMF, OP, OPc string
	// The outputs of f1, f1*, f2, f5, f3, f4 and f5*.
	MACA, MACS, RES, AK, CK, IK, AKStar string
}

// testSets are the 20 sets of TS 35.208 4.3.1 to 4.3.20, copied from
// the hexadecimal tables of TS 35.208 V19.0.0.
var testSets = []testSet{
	{set: 1,
		K: "465b5ce8b199b49faa5f0a2ee238a6bc", RAND: "23553cbe9637a89d218ae64dae47bf35", SQN: "ff9bb4d0b607", AMF: "b9b9", OP: "cdc202d5123e20f62b6d676ac72cb318", OPc: "cd63cb71954a9f4e48a5994e37a02baf",
		MACA: "4a9ffac354dfafb3", MACS: "01cfaf9ec4e871e9", RES: "a54211d5e3ba50bf", AK: "aa689c648370", CK: "b40ba9a3c58b2a05bbf0d987b21bf8cb", IK: "f769bcd751044604127672711c6d3441", AKStar: "451e8beca43b"},
	{set: 2,
		K: "465b5ce8b199b49faa5f0a2ee238a6bc", RAND: "23553cbe9637a89d218ae64dae47bf35", SQN: "ff9bb4d0b607", AMF: "b9b9", OP: "cdc202d5123e20f62b6d676ac72cb318", OPc: "cd63cb71954a9f4e48a5994e37a02baf",
		MACA: "4a9ffac354dfafb3", MACS: "01cfaf9ec4e871e9", RES: "a54211d5e3ba50bf", AK: "aa689c648370", CK: "b40ba9a3c58b2a05bbf0d987b21bf8cb", IK: "f769bcd751044604127672711c6d3441", AKStar: "451e8beca43b"},
	{set: 3,
		K: "fec86ba6eb707ed08905757b1bb44b8f", RAND: "9f7c8d021accf4db213ccff0c7f71a6a", SQN: "9d0277595ffc", AMF: "725c", OP: "dbc59adcb6f9a0ef735477b7fadf8374", OPc: "1006020f0a478bf6b699f15c062e42b3",
		MACA: "9cabc3e99baf7281", MACS: "95814ba2b3044324", RES: "8011c48c0c214ed2", AK: "33484dc2136b", CK: "5dbdbb2954e8f3cde665b046179a5098", IK: "59a92d3b476a0443487055cf88b2307b", AKStar: "deacdd848cc6"},
	{set: 4,
		K: "9e5944aea94b81165c82fbf9f32db751", RAND: "ce83dbc54ac0274a157c17f80d017bd6", SQN: "0b604a81eca8", AMF: "9e09", OP: "223014c5806694c007ca1eeef57f004f", OPc: "a64a507ae1a2a98bb88eb4210135dc87",
		MACA: "74a58220cba84c49", MACS: "ac2cc74a96871837", RES: "f365cd683cd92e96", AK: "f0b9c08ad02e", CK: "e203edb3971574f5a94b0d61b816345d", IK: "0c4524adeac041c4dd830d20854fc46b", AKStar: "6085a86c6f63"},
	{set: 5,
		K: "4ab1deb05ca6ceb051fc98e77d026a84", RAND: "74b0cd6031a1c8339b2b6ce2b8c4a186", SQN: "e880a1b580b6", AMF: "9f07", OP: "2d16c5cd1fdf6b22383584e3bef2a8d8", OPc: "dcf07cbd51855290b92a07a9891e523e",
		MACA: "49e785dd12626ef2", MACS: "9e85790336bb3fa2", RES: "5860fc1bce351e7e", AK: "31e11a609118", CK: "7657766b373d1c2138f307e3de9242f9", IK: "1c42e960d89b8fa99f2744e0708ccb53", AKStar: "fe2555e54aa9"},
	{set: 6,
		K: "6c38a116ac280c454f59332ee35c8c4f", RAND: "ee6466bc96202c5a557abbeff8babf63", SQN: "414b98222181", AMF: "4464", OP: "1ba00a1a7c6700ac8c3ff3e96ad08725", OPc: "3803ef5363b947c6aaa225e58fae3934",
		MACA: "078adfb488241a57", MACS: "80246b8d0186bcf1", RES: "16c8233f05a0ac28", AK: "45b0f69ab06c", CK: "3f8c7587fe8e4b233af676aede30ba3b", IK: "a7466cc1e6b2a1337d49d3b66e95d7b4", AKStar: "1f53cd2b1113"},
	{set: 7,
		K: "2d609d4db0ac5bf0d2c0de267014de0d", RAND: "194aa756013896b74b4a2a3b0af4539e", SQN: "6bf69438c2e4", AMF: "5f67", OP: "460a48385427aa39264aac8efc9e73e8", OPc: "c35a0ab0bcbfc9252caff15f24efbde0",
		MACA: "bd07d3003b9e5cc3", MACS: "bcb6c2fcad152250", RES: "8c25a16cd918a1df", AK: "7e6455f34cf3", CK: "4cd0846020f8fa0731dd47cbdc6be411", IK: "88ab80a415f15c73711254a1d388f696", AKStar: "dc6dd01e8f15"},
	{set: 8,
		K: "a530a7fe428fad1082c45eddfce13884", RAND: "3a4c2b3245c50eb5c71d08639395764d", SQN: "f63f5d768784", AMF: "b90e", OP: "511c6c4e83e38c89b1c5d8dde62426fa", OPc: "27953e49bc8af6dcc6e730eb80286be3",
		MACA: "53761fbd679b0bad", MACS: "21adfd334a10e7ce", RES: "a63241e1ffc3e5ab", AK: "88196c47986f", CK: "10f05bab75a99a5fbb98a9c287679c3b", IK: "f9ec0865eb32f22369cade40c59c3a44", AKStar: "c987a3d23115"},
	{set: 9,
		K: "d9151cf04896e25830bf2e08267b8360", RAND: "f761e5e93d603feb730e27556cb8a2ca", SQN: "47ee0199820a", AMF: "9113", OP: "75fc2233a44294ee8e6de25c4353d26b", OPc: "c4c93effe8a08138c203d4c27ce4e3d9",
		MACA: "66cc4be44862af1f", MACS: "7a4b8d7a8753f246", RES: "4a90b2171ac83a76", AK: "82a0f5287a71", CK: "71236b7129f9b22ab77ea7a54c96da22", IK: "90527ebaa5588968db41727325a04d9e", AKStar: "527dbf41f35f"},
	{set: 10,
		K: "a0e2971b6822e8d354a18cc235624ecb", RAND: "08eff828b13fdb562722c65c7f30a9b2", SQN: "db5c066481e0", AMF: "716b", OP: "323792faca21fb4d5d6f13c145a9d2c1", OPc: "82a26f22bba9e9488f949a10d98e9cc4",
		MACA: "9485fe24621cb9f6", MACS: "bce325ce03e2e9b9", RES: "4bc2212d8624910a", AK: "a2f858aa9e5d", CK: "08cef6d004ec61471a3c3cda048137fa", IK: "ed0318ca5deb9206272f6e8fa64ba411", AKStar: "74e76fbbec38"},
	{set: 11,
		K: "0da6f7ba86d5eac8a19cf563ac58642d", RAND: "679ac4dbacd7d233ff9d6806f4149ce3", SQN: "6e2331d692ad", AMF: "224a", OP: "4b9a26fa459e3acbff36f4015de3bdc1", OPc: "0db1071f8767562ca43a0a64c41e8d08",
		MACA: "2831d7ae9088e492", MACS: "9b2e16951135d523", RES: "6fc30fee6d123523", AK: "4c539a26e1fa", CK: "69b1cae7c7429d975e245cacb05a517c", IK: "74f24e8c26df58e1b38d7dcd4f1b7fbd", AKStar: "07861e126928"},
	{set: 12,
		K: "77b45843c88e58c10d202684515ed430", RAND: "4c47eb3076dc55fe5106cb2034b8cd78", SQN: "fe1a8731005d", AMF: "ad25", OP: "bf3286c7a51409ce95724d503bfe6e70", OPc: "d483afae562409a326b5bb0b20c4d762",
		MACA: "08332d7e9f484570", MACS: "ed41b734489d5207", RES: "aefa357beac2a87a", AK: "30ff25cdadf6", CK: "908c43f0569cb8f74bc971e706c36c5f", IK: "c251df0d888dd9329bcf46655b226e40", AKStar: "e84ed0d4677e"},
	{set: 13,
		K: "729b17729270dd87ccdf1bfe29b4e9bb", RAND: "311c4c929744d675b720f3b7e9b1cbd0", SQN: "c85c4cf65916", AMF: "5bb2", OP: "d04c9c35bd2262fa810d2924d036fd13", OPc: "228c2f2f06ac3268a9e616ee16db4ba1",
		MACA: "ff794fe2f827ebf8", MACS: "24fe4dc61e874b52", RES: "98dbbd099b3b408d", AK: "5380d158cfe3", CK: "44c0f23c5493cfd241e48f197e1d1012", IK: "0c9fb81613884c2535dd0eabf3b440d8", AKStar: "87ac3b559fb6"},
	{set: 14,
		K: "d32dd23e89dc662354ca12eb79dd32fa", RAND: "cf7d0ab1d94306950bf12018fbd46887", SQN: "484107e56a43", AMF: "b5e6", OP: "fe75905b9da47d356236d0314e09c32e", OPc: "d22a4b4180a5325708a5ff70d9f67ec7",
		MACA: "cf19d62b6a809866", MACS: "5d269537e45e2ce6", RES: "af4a411e1139f2c2", AK: "217af49272ad", CK: "5af86b80edb70df5292cc1121cbad50c", IK: "7f4d6ae7440e18789a8b75ad3f42f03a", AKStar: "900e101c677e"},
	{set: 15,
		K: "af7c65e1927221de591187a2c5987a53", RAND: "1f0f8578464fd59b64bed2d09436b57a", SQN: "3d627b01418d", AMF: "84f6", OP: "0c7acb8d95b7d4a31c5aca6d26345a88", OPc: "a4cf5c8155c08a7eff418e5443b98e55",
		MACA: "c37cae7805642032", MACS: "68cd09a452d8db7c", RES: "7bffa5c2f41fbc05", AK: "837fd7b74419", CK: "3f8c3f3ccf7625bf77fc94bcfd22fd26", IK: "abcbae8fd46115e9961a55d0da5f2078", AKStar: "56e97a6090b1"},
	{set: 16,
		K: "5bd7ecd3d3127a41d12539bed4e7cf71", RAND: "59b75f14251c75031d0bcbac1c2c04c7", SQN: "a298ae8929dc", AMF: "d056", OP: "f967f76038b920a9cd25e10c08b49924", OPc: "76089d3c0ff3efdc6e36721d4fceb747",
		MACA: "c3f25cd94309107e", MACS: "b0c8ba343665afcc", RES: "7e3f44c7591f6f45", AK: "5be11495525d", CK: "d42b2d615e49a03ac275a5aef97af892", IK: "0b3f8d024fe6bfafaa982b8f82e319c2", AKStar: "4d6a34a1e4eb"},
	{set: 17,
		K: "6cd1c6ceb1e01e14f1b82316a90b7f3d", RAND: "f69b78f300a0568bce9f0cb93c4be4c9", SQN: "b4fce5feb059", AMF: "e4bb", OP: "078bfca9564659ecd8851e84e6c59b48", OPc: "a219dc37f1dc7d66738b5843c799f206",
		MACA: "69a90869c268cb7b", MACS: "2e0fdcf9fd1cfa6a", RES: "70f6bdb9ad21525f", AK: "1c408a858b3e", CK: "6edaf99e5bd9f85d5f36d91c1272fb4b", IK: "d61c853c280dd9c46f297baec386de17", AKStar: "aa4ae52daa30"},
	{set: 18,
		K: "b73a90cbcf3afb622dba83c58a8415df", RAND: "b120f1c1a0102a2f507dd543de68281f", SQN: "f1e8a523a36d", AMF: "471b", OP: "b672047e003bb952dca6cb8af0e5b779", OPc: "df0c67868fa25f748b7044c6e7c245b8",
		MACA: "ebd70341bcd415b0", MACS: "12359f5d82220c14", RES: "479dd25c20792d63", AK: "aefdaa5ddd99", CK: "66195dbed0313274c5ca7766615fa25e", IK: "66bec707eb2afc476d7408a8f2927b36", AKStar: "12ec2b87fbb1"},
	{set: 19,
		K: "5122250214c33e723a5dd523fc145fc0", RAND: "81e92b6c0ee0e12ebceba8d92a99dfa5", SQN: "16f3b3f70fc2", AMF: "c3ab", OP: "c9e8763286b5b9ffbdf56e1297d0887b", OPc: "981d464c7c52eb6e5036234984ad0bcf",
		MACA: "2a5c23d15ee351d5", MACS: "62dae3853f3af9d2", RES: "28d7b0f2a2ec3de5", AK: "ada15aeb7bb8", CK: "5349fbe098649f948f5d2e973a81c00f", IK: "9744871ad32bf9bbd1dd5ce54e3e2e5a", AKStar: "d461bc15475d"},
	{set: 20,
		K: "90dca4eda45b53cf0f12d7c9c3bc6a89", RAND: "9fddc72092c6ad036b6e464789315b78", SQN: "20f813bd4141", AMF: "61df", OP: "3ffcfe5b7b1111589920d3528e84e655", OPc: "cb9cccc4b9258e6dca4760379fb82581",
		MACA: "09db94eab4f8149e", MACS: "a29468aa9775b527", RES: "a95100e2760952cd", AK: "83cfd54db913", CK: "b5f2da03883b69f96bf52e029ed9ac45", IK: "b4721368bc16ea67875c5598688bb0ef", AKStar: "4f2039392ddc"},
}

func TestConformanceTestSets(t *testing.T) {
	for _, ts := range testSets {
		k, op, rand := block16(t, ts.K), block16(t, ts.OP), block16(t, ts.RAND)
		var sqn [6]byte
		var amf [2]byte
		decode(t, sqn[:], ts.SQN)
		decode(t, amf[:], ts.AMF)

		opc := OPc(k, op)
		check(t, ts.set, "OPc", opc[:], ts.OPc)

		m := New(k, opc)
		macA := m.F1(rand, sqn, amf)
		macS := m.F1Star(rand, sqn, amf)
		res := m.F2(rand)
		ck := m.F3(rand)
		ik := m.F4(rand)
		ak := m.F5(rand)
		akStar := m.F5Star(rand)
		check(t, ts.set, "f1", macA[:], ts.MACA)
		check(t, ts.set, "f1*", macS[:], ts.MACS)
		check(t, ts.set, "f2", res[:], ts.RES)
		check(t, ts.set, "f3", ck[:], ts.CK)
		check(t, ts.set, "f4", ik[:], ts.IK)
		check(t, ts.set, "f5", ak[:], ts.AK)
		check(t, ts.set, "f5*", akStar[:], ts.AKStar)
	}
}

func block16(t *testing.T, s string) [16]byte {
	var b [16]byte
	decode(t, b[:], s)
	return b
}

func decode(t *testing.T, dst []byte, s string) {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != len(dst) {
		t.Fatalf("test data %q: %d bytes, %v; want %d bytes", s, len(b), err, len(dst))
	}
	copy(dst, b)
}

func check(t *testing.T, set int, name string, got []byte, want string) {
	t.Helper()
	if hex.EncodeToString(got) != want {
		t.Errorf("test set %d: %s = %x, want %s", set, name, got, want)
	}
}
