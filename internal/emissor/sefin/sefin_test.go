package sefin

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const nfseXML = `<NFSe xmlns="http://www.sped.fazenda.gov.br/nfse"><infNFSe Id="NFS123"/></NFSe>`

func TestCompactarDescompactar(t *testing.T) {
	b64, err := Compactar([]byte(nfseXML))
	if err != nil {
		t.Fatal(err)
	}
	volta, err := Descompactar(b64)
	if err != nil {
		t.Fatal(err)
	}
	if string(volta) != nfseXML {
		t.Fatalf("ida e volta diferentes: %s", volta)
	}
	if _, err := Descompactar("não é base64"); err == nil {
		t.Error("base64 inválido deveria falhar")
	}
	if _, err := Descompactar("aGVsbG8="); err == nil {
		t.Error("conteúdo sem gzip deveria falhar")
	}
}

func TestEmitirSucesso(t *testing.T) {
	var recebido []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/SefinNacional/nfse" {
			t.Errorf("requisição inesperada: %s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		var body struct {
			DpsXMLGZipB64 string `json:"dpsXmlGZipB64"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		recebido, _ = Descompactar(body.DpsXMLGZipB64)
		gz, _ := Compactar([]byte(nfseXML))
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"tipoAmbiente":          2,
			"versaoAplicativo":      "SefinNacional_1.6.0",
			"dataHoraProcessamento": "2026-11-21T13:30:20-03:00",
			"idDps":                 "DPS520140521122233300018100001000000000000042",
			"chaveAcesso":           "52014052211222333000181000000000004226119123456789",
			"nfseXmlGZipB64":        gz,
			"alertas":               []map[string]string{{"codigo": "A001", "descricao": "alerta de teste"}},
		})
	}))
	defer srv.Close()

	c := NovoComHTTP(srv.URL+"/SefinNacional/", srv.Client())
	nota, err := c.Emitir(context.Background(), []byte("<DPS/>"))
	if err != nil {
		t.Fatal(err)
	}
	if string(recebido) != "<DPS/>" {
		t.Errorf("DPS recebida pela Sefin = %q", recebido)
	}
	if nota.ChaveAcesso != "52014052211222333000181000000000004226119123456789" || nota.Ambiente != 2 {
		t.Errorf("nota inesperada: %+v", nota)
	}
	if string(nota.XML) != nfseXML {
		t.Errorf("XML da nota = %s", nota.XML)
	}
	if len(nota.Alertas) != 1 || nota.Alertas[0].String() != "A001 - alerta de teste" {
		t.Errorf("alertas = %+v", nota.Alertas)
	}
}

func TestEmitirRejeitada(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// A Sefin já foi vista devolvendo as chaves com inicial maiúscula.
		io.WriteString(w, `{"tipoAmbiente":2,"idDPS":"DPS1","erros":[{"Codigo":"E0004","Descricao":"Identificador difere"},{"codigo":"E0714","descricao":"Erro na assinatura"}]}`)
	}))
	defer srv.Close()

	_, err := NovoComHTTP(srv.URL, srv.Client()).Emitir(context.Background(), []byte("<DPS/>"))
	rej, ok := EhRejeicao(err)
	if !ok {
		t.Fatalf("esperava Rejeicao, veio %v", err)
	}
	if rej.Status != 400 || rej.IDDPS != "DPS1" || len(rej.Erros) != 2 || rej.Erros[0].Codigo != "E0004" {
		t.Errorf("rejeição inesperada: %+v", rej)
	}
	if !strings.Contains(err.Error(), "E0004 - Identificador difere; E0714 - Erro na assinatura") {
		t.Errorf("mensagem = %s", err)
	}
}

func TestRespostasInesperadas(t *testing.T) {
	casos := map[string]struct {
		status int
		corpo  string
		trecho string
	}{
		"html 500":          {500, "<html>erro interno</html>", "HTTP 500: <html>erro interno</html>"},
		"erro único":        {403, `{"erro":{"codigo":"E403","descricao":"certificado sem permissão"}}`, "E403 - certificado sem permissão"},
		"200 sem json":      {201, "ok", "não é JSON"},
		"xml corrompido":    {201, `{"chaveAcesso":"X","nfseXmlGZipB64":"aGVsbG8="}`, "abrir XML"},
		"corpo muito longo": {502, strings.Repeat("x", 800), "..."},
	}
	for nome, cs := range casos {
		t.Run(nome, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(cs.status)
				io.WriteString(w, cs.corpo)
			}))
			defer srv.Close()
			_, err := NovoComHTTP(srv.URL, srv.Client()).Emitir(context.Background(), []byte("<DPS/>"))
			if err == nil || !strings.Contains(err.Error(), cs.trecho) {
				t.Fatalf("erro = %v, queria conter %q", err, cs.trecho)
			}
		})
	}
}

func TestConsultas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nfse/CHAVE1":
			gz, _ := Compactar([]byte(nfseXML))
			json.NewEncoder(w).Encode(map[string]any{"chaveAcesso": "CHAVE1", "nfseXmlGZipB64": gz})
		case "/dps/DPS1":
			json.NewEncoder(w).Encode(map[string]any{"idDps": "DPS1", "chaveAcesso": "CHAVE1"})
		default:
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"erros":[{"codigo":"E2404","descricao":"Não foi gerada uma NFS-e com o identificador de DPS informado"}]}`)
		}
	}))
	defer srv.Close()
	c := NovoComHTTP(srv.URL, srv.Client())

	n, err := c.ConsultarNFSe(context.Background(), "CHAVE1")
	if err != nil || string(n.XML) != nfseXML {
		t.Fatalf("consulta: %v %+v", err, n)
	}
	chave, err := c.ChavePorDPS(context.Background(), "DPS1")
	if err != nil || chave != "CHAVE1" {
		t.Fatalf("chave por DPS: %q %v", chave, err)
	}
	_, err = c.ChavePorDPS(context.Background(), "DPS2")
	if rej, ok := EhRejeicao(err); !ok || rej.Status != 404 || rej.Erros[0].Codigo != "E2404" {
		t.Fatalf("esperava E2404, veio %v", err)
	}
}

func TestFalhaDeRede(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := NovoComHTTP(srv.URL, srv.Client()).Emitir(context.Background(), []byte("<DPS/>"))
	if err == nil || !strings.Contains(err.Error(), "falha de comunicação") {
		t.Fatalf("erro = %v", err)
	}
	if _, ok := EhRejeicao(err); ok {
		t.Error("falha de rede não é rejeição")
	}
	if errors.Unwrap(err) == nil {
		t.Error("erro de rede deveria embrulhar a causa")
	}
}

// TestMTLS confere que o certificado do prestador é apresentado ao servidor.
func TestMTLS(t *testing.T) {
	cliente := certificadoCliente(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"chaveAcesso": r.TLS.PeerCertificates[0].Subject.CommonName})
	}))
	srv.TLS = &tls.Config{ClientAuth: tls.RequireAnyClientCert}
	srv.StartTLS()
	defer srv.Close()

	c := Novo(srv.URL, cliente)
	tr := c.http.Transport.(*http.Transport)
	tr.TLSClientConfig.RootCAs = x509.NewCertPool()
	tr.TLSClientConfig.RootCAs.AddCert(srv.Certificate())

	n, err := c.ConsultarNFSe(context.Background(), "X")
	if err != nil {
		t.Fatal(err)
	}
	if n.ChaveAcesso != "PRESTADOR:11222333000181" {
		t.Fatalf("servidor viu o certificado %q", n.ChaveAcesso)
	}
}

func certificadoCliente(t *testing.T) tls.Certificate {
	t.Helper()
	chave, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	modelo := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "PRESTADOR:11222333000181"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, modelo, modelo, &chave.PublicKey, chave)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: chave}
}
