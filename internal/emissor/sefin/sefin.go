// Package sefin é o cliente HTTP da API Sefin Nacional da NFS-e, usado pelo
// próprio prestador para emitir e consultar notas.
//
// A autenticação é mútua (mTLS) com o certificado ICP-Brasil do prestador. O
// XML vai e volta compactado em GZip e codificado em Base64 dentro do JSON.
package sefin

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endereços oficiais da Sefin Nacional.
const (
	URLProducao    = "https://sefin.nfse.gov.br/SefinNacional"
	URLHomologacao = "https://sefin.producaorestrita.nfse.gov.br/SefinNacional"
)

// Diagnostico, quando definido, recebe detalhes da conexão TLS (pedido de
// certificado pelo servidor, cadeia enviada). Útil para investigar HTTP 403.
var Diagnostico io.Writer

func diagnosticarPedido(info *tls.CertificateRequestInfo, cert tls.Certificate) {
	if Diagnostico == nil {
		return
	}
	fmt.Fprintf(Diagnostico, "[tls] o servidor pediu certificado do cliente; ele aceita %d autoridades\n", len(info.AcceptableCAs))
	aceitas := map[string]bool{}
	for _, ca := range info.AcceptableCAs {
		aceitas[string(ca)] = true
	}
	for i, der := range cert.Certificate {
		c, err := x509.ParseCertificate(der)
		if err != nil {
			continue
		}
		fmt.Fprintf(Diagnostico, "[tls] enviando cert %d: titular %q, emissor %q, emissor aceito pelo servidor: %v\n",
			i, c.Subject.CommonName, c.Issuer.CommonName, aceitas[string(c.RawIssuer)])
	}
	if len(cert.Certificate) == 1 {
		fmt.Fprintln(Diagnostico, "[tls] atenção: o .pfx tem só o certificado do titular, sem a cadeia das autoridades certificadoras")
	}
}

// Cliente fala com a Sefin Nacional.
type Cliente struct {
	base string
	http *http.Client
}

// Novo cria um cliente com mTLS usando o certificado do prestador.
// base é URLHomologacao ou URLProducao.
func Novo(base string, cert tls.Certificate) *Cliente {
	transporte := &http.Transport{
		TLSClientConfig: &tls.Config{
			// Sempre apresenta o certificado do prestador. Com o campo
			// Certificates, o Go só envia o certificado se o emissor dele
			// estiver na lista de autoridades que o servidor anuncia; a Sefin
			// anuncia uma lista que pode não bater com a cadeia exportada do
			// Windows, e aí nada é enviado e a resposta é HTTP 403.
			GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
				diagnosticarPedido(info, cert)
				return &cert, nil
			},
			MinVersion: tls.VersionTLS12,
			// A Sefin (IIS) só pede o certificado do cliente depois do
			// handshake, renegociando a conexão TLS 1.2. Sem isso o Go
			// recusa com "tls: no renegotiation".
			Renegotiation: tls.RenegotiateFreelyAsClient,
		},
		Proxy:               http.ProxyFromEnvironment,
		TLSHandshakeTimeout: 15 * time.Second,
	}
	return NovoComHTTP(base, &http.Client{Transport: transporte, Timeout: 90 * time.Second})
}

// NovoComHTTP cria um cliente com um http.Client já configurado (usado em testes).
func NovoComHTTP(base string, h *http.Client) *Cliente {
	return &Cliente{base: strings.TrimRight(base, "/"), http: h}
}

// Mensagem é um erro ou alerta devolvido pela Sefin.
type Mensagem struct {
	Mensagem    string `json:"mensagem"`
	Codigo      string `json:"codigo"`
	Descricao   string `json:"descricao"`
	Complemento string `json:"complemento"`
}

func (m Mensagem) String() string {
	partes := []string{}
	for _, p := range []string{m.Codigo, m.Descricao, m.Mensagem, m.Complemento} {
		if p = strings.TrimSpace(p); p != "" {
			partes = append(partes, p)
		}
	}
	return strings.Join(partes, " - ")
}

// Rejeicao é devolvida quando a Sefin recusa a requisição.
type Rejeicao struct {
	Status int
	IDDPS  string
	Erros  []Mensagem
	Corpo  string // resposta bruta, quando não veio no formato esperado
}

func (r *Rejeicao) Error() string {
	if len(r.Erros) == 0 {
		corpo := r.Corpo
		if len(corpo) > 500 {
			corpo = corpo[:500] + "..."
		}
		return fmt.Sprintf("Sefin respondeu HTTP %d: %s", r.Status, corpo)
	}
	msgs := make([]string, len(r.Erros))
	for i, e := range r.Erros {
		msgs[i] = e.String()
	}
	return fmt.Sprintf("nota rejeitada (HTTP %d): %s", r.Status, strings.Join(msgs, "; "))
}

// NotaAutorizada é o resultado de uma emissão ou consulta bem-sucedida.
type NotaAutorizada struct {
	Ambiente      int
	VersaoApp     string
	Processamento string
	IDDPS         string
	ChaveAcesso   string
	XML           []byte // XML da NFS-e já descompactado
	Alertas       []Mensagem
}

type respostaNFSe struct {
	TipoAmbiente          int        `json:"tipoAmbiente"`
	VersaoAplicativo      string     `json:"versaoAplicativo"`
	DataHoraProcessamento string     `json:"dataHoraProcessamento"`
	IDDps                 string     `json:"idDps"`
	ChaveAcesso           string     `json:"chaveAcesso"`
	NFSeXMLGZipB64        string     `json:"nfseXmlGZipB64"`
	Alertas               []Mensagem `json:"alertas"`
	Erros                 []Mensagem `json:"erros"`
	Erro                  *Mensagem  `json:"erro"`
}

// Emitir envia a DPS assinada e devolve a NFS-e autorizada.
func (c *Cliente) Emitir(ctx context.Context, dpsAssinada []byte) (*NotaAutorizada, error) {
	payload, err := Compactar(dpsAssinada)
	if err != nil {
		return nil, err
	}
	corpo, _ := json.Marshal(map[string]string{"dpsXmlGZipB64": payload})
	return c.nota(ctx, http.MethodPost, "/nfse", corpo, http.StatusCreated)
}

// ConsultarNFSe busca uma NFS-e pela chave de acesso de 50 posições.
func (c *Cliente) ConsultarNFSe(ctx context.Context, chave string) (*NotaAutorizada, error) {
	return c.nota(ctx, http.MethodGet, "/nfse/"+url.PathEscape(chave), nil, http.StatusOK)
}

// ChavePorDPS devolve a chave de acesso da NFS-e gerada a partir de uma DPS.
// Serve para descobrir se uma DPS enviada (por exemplo, numa conexão que caiu)
// já virou nota antes de reenviar.
func (c *Cliente) ChavePorDPS(ctx context.Context, idDPS string) (string, error) {
	n, err := c.nota(ctx, http.MethodGet, "/dps/"+url.PathEscape(idDPS), nil, http.StatusOK)
	if err != nil {
		return "", err
	}
	return n.ChaveAcesso, nil
}

func (c *Cliente) nota(ctx context.Context, metodo, caminho string, corpo []byte, sucesso int) (*NotaAutorizada, error) {
	var leitor io.Reader
	if corpo != nil {
		leitor = bytes.NewReader(corpo)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, c.base+caminho, leitor)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("falha de comunicação com a Sefin: %w", err)
	}
	defer resp.Body.Close()
	bruto, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, fmt.Errorf("ler resposta da Sefin: %w", err)
	}

	var r respostaNFSe
	jsonOK := json.Unmarshal(bruto, &r) == nil

	if resp.StatusCode != sucesso && !(sucesso == http.StatusCreated && resp.StatusCode == http.StatusOK) {
		rej := &Rejeicao{Status: resp.StatusCode, IDDPS: r.IDDps}
		if jsonOK {
			rej.Erros = r.Erros
			if r.Erro != nil {
				rej.Erros = append(rej.Erros, *r.Erro)
			}
		}
		if len(rej.Erros) == 0 {
			rej.Corpo = strings.TrimSpace(string(bruto))
		}
		return nil, rej
	}
	if !jsonOK {
		return nil, fmt.Errorf("resposta da Sefin não é JSON válido: %.300s", bruto)
	}

	n := &NotaAutorizada{
		Ambiente:      r.TipoAmbiente,
		VersaoApp:     r.VersaoAplicativo,
		Processamento: r.DataHoraProcessamento,
		IDDPS:         r.IDDps,
		ChaveAcesso:   r.ChaveAcesso,
		Alertas:       r.Alertas,
	}
	if r.NFSeXMLGZipB64 != "" {
		if n.XML, err = Descompactar(r.NFSeXMLGZipB64); err != nil {
			return nil, fmt.Errorf("abrir XML da NFS-e: %w", err)
		}
	}
	return n, nil
}

// Compactar aplica GZip e Base64, como a Sefin espera no campo dpsXmlGZipB64.
func Compactar(xml []byte) (string, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(xml); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Descompactar desfaz Compactar.
func Descompactar(b64 string) ([]byte, error) {
	gz, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, 50<<20))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// EhRejeicao informa se err é uma recusa da Sefin (e não uma falha de rede).
func EhRejeicao(err error) (*Rejeicao, bool) {
	var r *Rejeicao
	ok := errors.As(err, &r)
	return r, ok
}
