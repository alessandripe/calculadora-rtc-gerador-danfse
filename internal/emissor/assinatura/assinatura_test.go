package assinatura

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
	"github.com/russellhaering/goxmldsig/etreeutils"
	"software.sslmate.com/src/go-pkcs12"
)

const dpsExemplo = `<DPS xmlns="http://www.sped.fazenda.gov.br/nfse" versao="1.01"><infDPS Id="DPS520140521122233300018100001000000000000042"><tpAmb>2</tpAmb><xDescServ>Honorários d'Ávila &amp; "sócios"</xDescServ></infDPS></DPS>`

// pfxDeTeste cria um certificado autoassinado no formato de um e-CNPJ e o
// devolve como .pfx protegido pela senha informada.
func pfxDeTeste(t *testing.T, senha string, validade time.Duration) []byte {
	t.Helper()
	chave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	modelo := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ESCRITORIO DE ADVOCACIA TESTE:11222333000181"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validade),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, modelo, modelo, &chave.PublicKey, chave)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pfx, err := pkcs12.Modern.Encode(chave, cert, nil, senha)
	if err != nil {
		t.Fatal(err)
	}
	return pfx
}

func certificadoDeTeste(t *testing.T) *Certificado {
	t.Helper()
	c, err := DecodificarPFX(pfxDeTeste(t, "senha-teste", 24*time.Hour), "senha-teste")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCarregarPFX(t *testing.T) {
	arq := filepath.Join(t.TempDir(), "cert.pfx")
	if err := os.WriteFile(arq, pfxDeTeste(t, "123", time.Hour), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := CarregarPFX(arq, "123")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.CNPJ(); got != "11222333000181" {
		t.Errorf("CNPJ = %q", got)
	}
	if err := c.Validade(time.Now()); err != nil {
		t.Errorf("certificado deveria estar válido: %v", err)
	}
	if err := c.Validade(time.Now().Add(48 * time.Hour)); err == nil {
		t.Error("deveria acusar certificado vencido")
	}
	if err := c.Validade(time.Now().Add(-48 * time.Hour)); err == nil {
		t.Error("deveria acusar certificado ainda não válido")
	}
	if len(c.TLS().Certificate) != 1 {
		t.Error("certificado TLS deveria ter a folha")
	}

	if _, err := CarregarPFX(arq, "errada"); err == nil {
		t.Error("senha errada deveria falhar")
	}
	if _, err := CarregarPFX(filepath.Join(t.TempDir(), "nao-existe.pfx"), "123"); err == nil {
		t.Error("arquivo inexistente deveria falhar")
	}
}

func TestAssinarDPS(t *testing.T) {
	for _, alg := range []Algoritmo{SHA1, SHA256} {
		c := certificadoDeTeste(t)
		assinado, err := c.AssinarDPS([]byte(dpsExemplo), alg)
		if err != nil {
			t.Fatal(err)
		}
		s := string(assinado)

		if strings.HasPrefix(s, "<?xml") || strings.Contains(s, "\n") {
			t.Errorf("XML assinado não deve ter declaração nem quebras de linha:\n%s", s)
		}
		if !strings.Contains(s, `</infDPS><Signature xmlns="http://www.w3.org/2000/09/xmldsig#">`) {
			t.Errorf("Signature deve vir logo após infDPS, sem prefixo:\n%s", s)
		}
		if strings.Contains(s, "ds:") {
			t.Errorf("Signature não deve usar prefixo ds:\n%s", s)
		}
		metodo := map[Algoritmo]string{SHA1: "xmldsig#rsa-sha1", SHA256: "xmldsig-more#rsa-sha256"}[alg]
		for _, trecho := range []string{
			`<CanonicalizationMethod Algorithm="http://www.w3.org/TR/2001/REC-xml-c14n-20010315"/>`,
			metodo,
			`<Reference URI="#DPS520140521122233300018100001000000000000042">`,
			`<Transform Algorithm="http://www.w3.org/2000/09/xmldsig#enveloped-signature"/><Transform Algorithm="http://www.w3.org/TR/2001/REC-xml-c14n-20010315"/>`,
			`<X509Certificate>`,
		} {
			if !strings.Contains(s, trecho) {
				t.Errorf("XML assinado não contém %s", trecho)
			}
		}

		verificar(t, c, assinado)

		// Qualquer alteração depois de assinar deve invalidar a assinatura.
		adulterado := []byte(strings.Replace(s, "<tpAmb>2</tpAmb>", "<tpAmb>1</tpAmb>", 1))
		if verificarErro(c, adulterado) == nil {
			t.Error("assinatura deveria falhar com o XML alterado")
		}
	}
}

func TestAssinarDPSErros(t *testing.T) {
	c := certificadoDeTeste(t)
	casos := map[string]string{
		"xml inválido":   `<DPS><infDPS`,
		"raiz errada":    `<NFSe><infDPS Id="x"/></NFSe>`,
		"sem infDPS":     `<DPS><outro/></DPS>`,
		"sem Id":         `<DPS><infDPS/></DPS>`,
		"já assinado":    `<DPS><infDPS Id="x"/><Signature/></DPS>`,
		"conteúdo vazio": ``,
	}
	for nome, xml := range casos {
		if _, err := c.AssinarDPS([]byte(xml), SHA1); err == nil {
			t.Errorf("%s: esperava erro", nome)
		}
	}
}

func verificar(t *testing.T, c *Certificado, xml []byte) {
	t.Helper()
	if err := verificarErro(c, xml); err != nil {
		t.Fatalf("assinatura inválida: %v\n%s", err, xml)
	}
}

// verificarErro confere a assinatura de forma direta: recalcula o digest de
// infDPS com C14N inclusiva, compara com o DigestValue e valida o
// SignatureValue sobre o SignedInfo canonicalizado, usando a chave pública do
// titular. O algoritmo de hash vem do próprio SignatureMethod.
func verificarErro(c *Certificado, xml []byte) error {
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(xml); err != nil {
		return err
	}
	root := doc.Root()
	sig := root.SelectElement("Signature")
	inf := root.SelectElement("infDPS")
	if sig == nil || inf == nil {
		return errors.New("Signature ou infDPS ausente")
	}
	si := sig.SelectElement("SignedInfo")
	ref := si.SelectElement("Reference")
	if ref.SelectAttrValue("URI", "") != "#"+inf.SelectAttrValue("Id", "") {
		return errors.New("Reference não aponta para infDPS")
	}

	hash := crypto.SHA1
	if strings.HasSuffix(si.SelectElement("SignatureMethod").SelectAttrValue("Algorithm", ""), "rsa-sha256") {
		hash = crypto.SHA256
	}
	c14n := dsig.MakeC14N10RecCanonicalizer()

	canonInf, err := c14n.Canonicalize(inf)
	if err != nil {
		return err
	}
	h := hash.New()
	h.Write(canonInf)
	if base64.StdEncoding.EncodeToString(h.Sum(nil)) != ref.SelectElement("DigestValue").Text() {
		return errors.New("DigestValue não confere")
	}

	ctx, err := etreeutils.NSBuildParentContext(si)
	if err != nil {
		return err
	}
	destacado, err := etreeutils.NSDetatch(ctx, si)
	if err != nil {
		return err
	}
	canonSI, err := c14n.Canonicalize(destacado)
	if err != nil {
		return err
	}
	h = hash.New()
	h.Write(canonSI)
	valor, err := base64.StdEncoding.DecodeString(sig.SelectElement("SignatureValue").Text())
	if err != nil {
		return err
	}
	return rsa.VerifyPKCS1v15(&c.Chave.PublicKey, hash, h.Sum(nil), valor)
}
