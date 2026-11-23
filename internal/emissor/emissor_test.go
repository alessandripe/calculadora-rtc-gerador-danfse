package emissor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"danfse-service/internal/emissor/dps"
)

func escritorioTeste() Escritorio {
	var e Escritorio
	e.CNPJ = "11222333000181"
	e.AplicarPadroes()
	return e
}

func notaTeste() Nota {
	var n Nota
	n.Tomador.CNPJ = "11.444.777/0001-61"
	n.Tomador.Nome = "Empresa Cliente Ltda"
	n.Tomador.Endereco = &dps.Endereco{CodigoMunicipio: "5208707", CEP: "74.000-000", Logradouro: "Av. Goiás", Numero: "1", Bairro: "Centro"}
	n.Descricao = "Honorários"
	n.Valor = 3000
	n.ISSRetido = true
	return n
}

func TestAplicarPadroes(t *testing.T) {
	e := escritorioTeste()
	if e.CodigoMunicipio != CodigoAparecidaDeGoiania || e.Serie != "1" || e.Simples.Opcao != 3 ||
		e.Simples.RegimeApuracao != 1 || e.Servico.CodigoTributacaoNacional != CodigoAdvocacia {
		t.Fatalf("padrões inesperados: %+v", e)
	}
}

func TestMontarDeclaracao(t *testing.T) {
	agora := time.Date(2026, 11, 21, 16, 30, 0, 0, time.UTC)
	d, err := MontarDeclaracao(escritorioTeste(), notaTeste(), dps.Homologacao, 7, agora)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Emissao.Format(time.RFC3339); got != "2026-11-21T13:29:00-03:00" {
		t.Errorf("emissão = %s (deveria ser 1 minuto antes, no fuso de Brasília)", got)
	}
	if d.Competencia.Format("2006-01-02") != "2026-11-21" {
		t.Errorf("competência = %s", d.Competencia)
	}
	if d.Tomador.CNPJ != "11444777000161" || d.Tomador.Endereco.CEP != "74000000" {
		t.Errorf("documentos deveriam ficar só com dígitos: %+v %+v", d.Tomador, d.Tomador.Endereco)
	}
	if d.Tributacao.RetencaoISSQN != 2 {
		t.Error("ISS retido deveria virar tpRetISSQN = 2")
	}

	n := notaTeste()
	n.Competencia = "2026-10-31"
	if d, err = MontarDeclaracao(escritorioTeste(), n, dps.Homologacao, 7, agora); err != nil || d.Competencia.Day() != 31 {
		t.Fatalf("competência informada: %v", err)
	}
	n.Competencia = "31/10/2026"
	if _, err := MontarDeclaracao(escritorioTeste(), n, dps.Homologacao, 7, agora); err == nil {
		t.Error("competência em formato errado deveria falhar")
	}
	n = notaTeste()
	n.Valor = 0
	if _, err := MontarDeclaracao(escritorioTeste(), n, dps.Homologacao, 7, agora); err == nil {
		t.Error("valor zero deveria falhar")
	}
}

func TestNumeracao(t *testing.T) {
	arq := filepath.Join(t.TempDir(), "numeracao.json")
	n, err := AbrirNumeracao(arq)
	if err != nil {
		t.Fatal(err)
	}
	for want := int64(1); want <= 3; want++ {
		got, err := n.Reservar(dps.Homologacao, "1")
		if err != nil || got != want {
			t.Fatalf("Reservar = %d, %v; quero %d", got, err, want)
		}
	}
	if got, _ := n.Reservar(dps.Producao, "1"); got != 1 {
		t.Errorf("produção tem numeração própria; veio %d", got)
	}

	// Reabrindo o arquivo, a contagem continua.
	n2, err := AbrirNumeracao(arq)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := n2.Reservar(dps.Homologacao, "1"); got != 4 {
		t.Errorf("depois de reabrir, quero 4; veio %d", got)
	}

	os.WriteFile(arq, []byte("{"), 0o600)
	if _, err := AbrirNumeracao(arq); err == nil {
		t.Error("arquivo corrompido deveria falhar")
	}
}

func TestLerJSON(t *testing.T) {
	dir := t.TempDir()
	arq := filepath.Join(dir, "e.json")
	os.WriteFile(arq, []byte(`{"cnpj":"11222333000181","serie":"2"}`), 0o600)
	var e Escritorio
	if err := LerJSON(arq, &e); err != nil || e.Serie != "2" {
		t.Fatalf("LerJSON: %v %+v", err, e)
	}
	os.WriteFile(arq, []byte(`{"cnpjj":"1"}`), 0o600)
	if err := LerJSON(arq, &e); err == nil {
		t.Error("campo desconhecido deveria falhar")
	}
	if err := LerJSON(filepath.Join(dir, "nao-existe.json"), &e); err == nil {
		t.Error("arquivo inexistente deveria falhar")
	}
}
