// Package assinatura carrega o certificado digital A1 (arquivo .pfx/.p12) e
// assina a DPS no padrão XMLDSig exigido pela NFS-e Nacional.
//
// A assinatura segue o que o Sistema Nacional aceita hoje: canonicalização
// C14N 1.0 inclusiva, transformações enveloped-signature + C14N, referência
// ao atributo Id de infDPS e o elemento Signature, sem prefixo, como último
// filho de DPS (irmão de infDPS).
package assinatura

import (
	"crypto/rsa"
	_ "crypto/sha1" // registra o SHA-1 usado pela assinatura rsa-sha1
	_ "crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/beevik/etree"
	dsig "github.com/russellhaering/goxmldsig"
	"software.sslmate.com/src/go-pkcs12"
)

// Algoritmo de hash da assinatura.
type Algoritmo int

const (
	// SHA1 (rsa-sha1) é o algoritmo usado pelas bibliotecas que já emitem em
	// produção no Sistema Nacional.
	SHA1 Algoritmo = iota
	// SHA256 (rsa-sha256), disponível caso a Sefin passe a exigir.
	SHA256
)

// Certificado é um e-CNPJ A1 carregado em memória.
type Certificado struct {
	Chave  *rsa.PrivateKey
	Folha  *x509.Certificate
	Cadeia []*x509.Certificate
}

// CarregarPFX lê um arquivo .pfx/.p12 protegido por senha.
func CarregarPFX(caminho, senha string) (*Certificado, error) {
	dados, err := os.ReadFile(caminho)
	if err != nil {
		return nil, fmt.Errorf("ler certificado: %w", err)
	}
	return DecodificarPFX(dados, senha)
}

// DecodificarPFX interpreta o conteúdo de um .pfx/.p12.
func DecodificarPFX(dados []byte, senha string) (*Certificado, error) {
	chave, folha, cadeia, err := pkcs12.DecodeChain(dados, senha)
	if err != nil {
		return nil, fmt.Errorf("abrir certificado (confira a senha): %w", err)
	}
	rsaKey, ok := chave.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("o certificado precisa ter chave RSA, como os e-CNPJ ICP-Brasil")
	}
	return &Certificado{Chave: rsaKey, Folha: folha, Cadeia: cadeia}, nil
}

// Validade devolve um erro se o certificado estiver vencido ou ainda não for válido em t.
func (c *Certificado) Validade(t time.Time) error {
	if t.Before(c.Folha.NotBefore) {
		return fmt.Errorf("certificado só é válido a partir de %s", c.Folha.NotBefore.Format("02/01/2006"))
	}
	if t.After(c.Folha.NotAfter) {
		return fmt.Errorf("certificado venceu em %s", c.Folha.NotAfter.Format("02/01/2006"))
	}
	return nil
}

// CNPJ tenta extrair o CNPJ do titular. Os e-CNPJ ICP-Brasil trazem o CNPJ
// no fim do Common Name ("RAZAO SOCIAL:11222333000181"). Retorna "" se não achar.
func (c *Certificado) CNPJ() string {
	cn := c.Folha.Subject.CommonName
	if i := strings.LastIndex(cn, ":"); i >= 0 {
		cand := cn[i+1:]
		if len(cand) == 14 && strings.Trim(cand, "0123456789") == "" {
			return cand
		}
	}
	return ""
}

// TLS devolve o certificado no formato usado pelo cliente HTTP (mTLS).
func (c *Certificado) TLS() tls.Certificate {
	cert := tls.Certificate{PrivateKey: c.Chave, Leaf: c.Folha}
	cert.Certificate = append(cert.Certificate, c.Folha.Raw)
	for _, ca := range c.Cadeia {
		cert.Certificate = append(cert.Certificate, ca.Raw)
	}
	return cert
}

// AssinarDPS assina o elemento infDPS e devolve o XML completo da DPS
// assinada, sem declaração <?xml?> e sem espaços entre elementos.
func (c *Certificado) AssinarDPS(xmlDPS []byte, alg Algoritmo) ([]byte, error) {
	return c.assinar(xmlDPS, "DPS", "infDPS", alg)
}

// assinar assina o filho `alvo` do elemento raiz `raiz` e acrescenta a
// Signature ao fim da raiz.
func (c *Certificado) assinar(conteudo []byte, raiz, alvo string, alg Algoritmo) ([]byte, error) {
	doc := etree.NewDocument()
	doc.ReadSettings.PreserveCData = true
	if err := doc.ReadFromBytes(conteudo); err != nil {
		return nil, fmt.Errorf("ler XML para assinar: %w", err)
	}
	root := doc.Root()
	if root == nil || root.Tag != raiz {
		return nil, fmt.Errorf("elemento raiz esperado: %s", raiz)
	}
	el := root.SelectElement(alvo)
	if el == nil {
		return nil, fmt.Errorf("elemento %s não encontrado", alvo)
	}
	if el.SelectAttrValue("Id", "") == "" {
		return nil, fmt.Errorf("elemento %s sem atributo Id", alvo)
	}
	if root.SelectElement("Signature") != nil {
		return nil, errors.New("o XML já está assinado")
	}

	ctx := dsig.NewDefaultSigningContext(keyStore{c})
	ctx.Prefix = ""
	ctx.IdAttribute = "Id"
	ctx.Canonicalizer = dsig.MakeC14N10RecCanonicalizer()
	metodo := dsig.RSASHA1SignatureMethod
	if alg == SHA256 {
		metodo = dsig.RSASHA256SignatureMethod
	}
	if err := ctx.SetSignatureMethod(metodo); err != nil {
		return nil, err
	}
	sig, err := ctx.ConstructSignature(el, true)
	if err != nil {
		return nil, fmt.Errorf("assinar %s: %w", alvo, err)
	}
	root.AddChild(sig)

	doc.WriteSettings.CanonicalText = true
	doc.WriteSettings.CanonicalAttrVal = true
	// Remove a declaração <?xml?> se houver: a Sefin recebe só o elemento DPS.
	for _, tok := range doc.Child {
		if p, ok := tok.(*etree.ProcInst); ok {
			doc.RemoveChild(p)
		}
	}
	out, err := doc.WriteToBytes()
	if err != nil {
		return nil, fmt.Errorf("serializar XML assinado: %w", err)
	}
	return out, nil
}

// keyStore adapta o Certificado à interface do goxmldsig. Só o certificado do
// titular vai no KeyInfo, como no Emissor Nacional.
type keyStore struct{ c *Certificado }

func (k keyStore) GetKeyPair() (*rsa.PrivateKey, []byte, error) {
	return k.c.Chave, k.c.Folha.Raw, nil
}
