package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danfse-service/internal/emissor/assinatura"
	"danfse-service/internal/emissor/dps"
	"danfse-service/internal/emissor/sefin"

	"software.sslmate.com/src/go-pkcs12"
)

// dirTeste é a pasta do pacote, guardada antes de qualquer t.Chdir.
var dirTeste, _ = os.Getwd()

var agoraFixo = func() time.Time { return time.Date(2026, 11, 21, 16, 30, 0, 0, time.UTC) }

// prepararPasta cria escritorio.json, nota.json e um certificado de teste
// numa pasta temporária e devolve o caminho dela.
func prepararPasta(t *testing.T, cnpjCert string) string {
	t.Helper()
	dir := t.TempDir()
	copiar := func(nome string) {
		dados, err := os.ReadFile(filepath.Join(dirTeste, "exemplo", nome))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, nome), dados, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	copiar("escritorio.json")
	copiar("nota.json")

	chave, _ := rsa.GenerateKey(rand.Reader, 2048)
	agora := agoraFixo()
	modelo := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ESCRITORIO TESTE:" + cnpjCert},
		NotBefore:    agora.Add(-24 * time.Hour),
		NotAfter:     agora.Add(365 * 24 * time.Hour),
	}
	der, _ := x509.CreateCertificate(rand.Reader, modelo, modelo, &chave.PublicKey, chave)
	cert, _ := x509.ParseCertificate(der)
	pfx, err := pkcs12.Modern.Encode(chave, cert, nil, "s3nha")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "certificado-a1.pfx"), pfx, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NFSE_CERT_SENHA", "s3nha")
	return dir
}

// sefinFalsa sobe um servidor que imita a Sefin e guarda a última DPS recebida.
func sefinFalsa(t *testing.T, responder func(w http.ResponseWriter, dps []byte)) *[]byte {
	t.Helper()
	var ultima []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/nfse":
			var body struct {
				DpsXMLGZipB64 string `json:"dpsXmlGZipB64"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			ultima, _ = sefin.Descompactar(body.DpsXMLGZipB64)
			responder(w, ultima)
		case strings.HasPrefix(r.URL.Path, "/nfse/"):
			gz, _ := sefin.Compactar([]byte("<NFSe/>"))
			json.NewEncoder(w).Encode(map[string]string{"chaveAcesso": strings.TrimPrefix(r.URL.Path, "/nfse/"), "nfseXmlGZipB64": gz})
		case strings.HasPrefix(r.URL.Path, "/dps/"):
			json.NewEncoder(w).Encode(map[string]string{"chaveAcesso": "CHAVE-DA-DPS"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	antes := novoCliente
	novoCliente = func(dps.Ambiente, *assinatura.Certificado) *sefin.Cliente {
		return sefin.NovoComHTTP(srv.URL, srv.Client())
	}
	t.Cleanup(func() { novoCliente = antes })
	return &ultima
}

func autoriza(w http.ResponseWriter, _ []byte) {
	gz, _ := sefin.Compactar([]byte(`<NFSe xmlns="http://www.sped.fazenda.gov.br/nfse"/>`))
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"tipoAmbiente":   2,
		"chaveAcesso":    "52014052211222333000181000000000000126119999999999",
		"nfseXmlGZipB64": gz,
		"alertas":        []map[string]string{{"codigo": "A1", "descricao": "teste"}},
	})
}

func rodar(t *testing.T, args ...string) (string, error) {
	t.Helper()
	// Roda numa pasta temporária para que a pasta de saída padrão ("notas")
	// e o registro.txt não sejam criados dentro do repositório.
	t.Chdir(t.TempDir())
	var out bytes.Buffer
	err := executar(context.Background(), args, &out, agoraFixo)
	return out.String(), err
}

func TestEmitirSemEnviar(t *testing.T) {
	dir := prepararPasta(t, "11222333000181")
	recebida := sefinFalsa(t, autoriza)
	saida := filepath.Join(dir, "notas")

	out, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"), "-saida", saida)
	if err != nil {
		t.Fatal(err)
	}
	if *recebida != nil {
		t.Fatal("nada deveria ter sido enviado sem -enviar")
	}
	if !strings.Contains(out, "DPS nº 1 (série 1, homologação)") || !strings.Contains(out, "Nada foi enviado") {
		t.Errorf("saída inesperada:\n%s", out)
	}
	arq := filepath.Join(saida, "homologacao", "DPS520140521122233300018100001000000000000001.xml")
	xml, err := os.ReadFile(arq)
	if err != nil {
		t.Fatal(err)
	}
	for _, trecho := range []string{
		"<tpAmb>2</tpAmb>", "<dhEmi>2026-11-21T13:29:00-03:00</dhEmi>", "<CPF>52998224725</CPF>",
		"<cTribNac>171401</cTribNac>", "<vServ>1500.00</vServ>", "<SignatureValue>",
	} {
		if !strings.Contains(string(xml), trecho) {
			t.Errorf("DPS não contém %s", trecho)
		}
	}
	validarSchema(t, xml)

	// A segunda emissão usa o número seguinte.
	if out, err = rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"), "-saida", saida); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "DPS nº 2") {
		t.Errorf("segunda emissão deveria ser a nº 2:\n%s", out)
	}
}

func TestEmitirEnviando(t *testing.T) {
	dir := prepararPasta(t, "11222333000181")
	recebida := sefinFalsa(t, autoriza)
	saida := filepath.Join(dir, "notas")

	out, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"), "-saida", saida, "-enviar")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(*recebida), `<infDPS Id="DPS520140521122233300018100001000000000000001">`) {
		t.Fatalf("Sefin recebeu:\n%s", *recebida)
	}
	if !strings.Contains(out, "NFS-e emitida em homologação. Chave de acesso: 52014052211222333000181000000000000126119999999999") ||
		!strings.Contains(out, "alerta da Sefin: A1 - teste") {
		t.Errorf("saída inesperada:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(saida, "homologacao", "nfse-52014052211222333000181000000000000126119999999999.xml")); err != nil {
		t.Error("XML da NFS-e não foi gravado")
	}
	reg, err := os.ReadFile(filepath.Join(saida, "registro.txt"))
	if err != nil || !strings.Contains(string(reg), "Chave de acesso: 52014052211222333000181000000000000126119999999999") {
		t.Errorf("registro.txt deveria ter o resultado: %v\n%s", err, reg)
	}
}

func TestEmitirRejeitada(t *testing.T) {
	dir := prepararPasta(t, "11222333000181")
	sefinFalsa(t, func(w http.ResponseWriter, _ []byte) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"erros":[{"codigo":"E0714","descricao":"Arquivo enviado com erro na assinatura."}]}`)
	})
	_, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"), "-saida", filepath.Join(dir, "n"), "-enviar")
	if err == nil || !strings.Contains(err.Error(), "E0714") || !strings.Contains(err.Error(), "não virou nota") {
		t.Fatalf("erro = %v", err)
	}
	if reg, _ := os.ReadFile(filepath.Join(dir, "n", "registro.txt")); !strings.Contains(string(reg), "erro: nota rejeitada") {
		t.Errorf("registro.txt deveria ter o erro:\n%s", reg)
	}
}

func TestEmitirFalhaDeRede(t *testing.T) {
	dir := prepararPasta(t, "11222333000181")
	antes := novoCliente
	t.Cleanup(func() { novoCliente = antes })
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	novoCliente = func(dps.Ambiente, *assinatura.Certificado) *sefin.Cliente {
		return sefin.NovoComHTTP(srv.URL, srv.Client())
	}
	_, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"), "-saida", filepath.Join(dir, "n"), "-enviar")
	if err == nil || !strings.Contains(err.Error(), "nfse-emissor dps -ambiente homologacao -id DPS5201405") {
		t.Fatalf("erro deveria orientar a consulta da DPS: %v", err)
	}
}

func TestEmitirErros(t *testing.T) {
	t.Run("certificado de outro CNPJ", func(t *testing.T) {
		dir := prepararPasta(t, "99888777000166")
		_, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"))
		if err == nil || !strings.Contains(err.Error(), "certificado é do CNPJ 99888777000166") {
			t.Fatalf("erro = %v", err)
		}
	})
	t.Run("sem senha", func(t *testing.T) {
		dir := prepararPasta(t, "11222333000181")
		os.Unsetenv("NFSE_CERT_SENHA")
		_, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"))
		if err == nil || !strings.Contains(err.Error(), "NFSE_CERT_SENHA") {
			t.Fatalf("erro = %v", err)
		}
	})
	t.Run("nota inválida não gasta número", func(t *testing.T) {
		dir := prepararPasta(t, "11222333000181")
		os.WriteFile(filepath.Join(dir, "nota.json"), []byte(`{"tomador":{"cpf":"123","nome":"X"},"descricao":"d","valor":10}`), 0o600)
		_, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"))
		if err == nil || !strings.Contains(err.Error(), "CPF do tomador") {
			t.Fatalf("erro = %v", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "numeracao.json")); err == nil {
			t.Error("número não deveria ter sido reservado")
		}
	})
	t.Run("campo desconhecido", func(t *testing.T) {
		dir := prepararPasta(t, "11222333000181")
		os.WriteFile(filepath.Join(dir, "nota.json"), []byte(`{"valr":10}`), 0o600)
		_, err := rodar(t, "emitir", "-config", filepath.Join(dir, "escritorio.json"), "-nota", filepath.Join(dir, "nota.json"))
		if err == nil || !strings.Contains(err.Error(), "valr") {
			t.Fatalf("erro = %v", err)
		}
	})
	t.Run("ambiente inválido", func(t *testing.T) {
		_, err := rodar(t, "emitir", "-ambiente", "teste")
		if err == nil || !strings.Contains(err.Error(), "ambiente deve ser") {
			t.Fatalf("erro = %v", err)
		}
	})
	t.Run("comando desconhecido", func(t *testing.T) {
		if _, err := rodar(t, "cancelar"); err == nil {
			t.Fatal("esperava erro")
		}
		if _, err := rodar(t); err == nil {
			t.Fatal("esperava erro sem comando")
		}
	})
}

func TestConsultas(t *testing.T) {
	dir := prepararPasta(t, "11222333000181")
	sefinFalsa(t, autoriza)
	cfg := filepath.Join(dir, "escritorio.json")

	out, err := rodar(t, "consultar", "-config", cfg, "-chave", "CHAVE1", "-saida", filepath.Join(dir, "n"))
	if err != nil || !strings.Contains(out, "NFS-e CHAVE1 gravada") {
		t.Fatalf("consultar: %v\n%s", err, out)
	}
	out, err = rodar(t, "dps", "-config", cfg, "-id", "DPS1")
	if err != nil || !strings.Contains(out, "gerou a NFS-e CHAVE-DA-DPS") {
		t.Fatalf("dps: %v\n%s", err, out)
	}
	if _, err := rodar(t, "consultar", "-config", cfg); err == nil {
		t.Error("consultar sem -chave deveria falhar")
	}
	if _, err := rodar(t, "dps", "-config", cfg); err == nil {
		t.Error("dps sem -id deveria falhar")
	}
	if out, err := rodar(t, "ajuda"); err != nil || !strings.Contains(out, "comandos:") {
		t.Error("ajuda deveria listar os comandos")
	}
}

// validarSchema valida a DPS assinada (com Signature) contra o schema oficial.
func validarSchema(t *testing.T, xml []byte) {
	t.Helper()
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint não instalado")
	}
	dir := t.TempDir()
	origem := filepath.Join(dirTeste, "..", "..", "internal", "emissor", "dps", "testdata", "schemas")
	arquivos, _ := os.ReadDir(origem)
	for _, a := range arquivos {
		c, _ := os.ReadFile(filepath.Join(origem, a.Name()))
		// Mesmo ajuste do pacote dps: "^" e "$" são literais em padrões XSD.
		c = []byte(strings.ReplaceAll(string(c), `"^0{0,4}\d{1,5}$"`, `"0{0,4}\d{1,5}"`))
		os.WriteFile(filepath.Join(dir, a.Name()), c, 0o600)
	}
	arq := filepath.Join(dir, "dps.xml")
	os.WriteFile(arq, xml, 0o600)
	if out, err := exec.Command(xmllint, "--nonet", "--noout", "--schema", filepath.Join(dir, "DPS_v1.01.xsd"), arq).CombinedOutput(); err != nil {
		t.Fatalf("DPS assinada inválida para o schema: %v\n%s", err, out)
	}
}
