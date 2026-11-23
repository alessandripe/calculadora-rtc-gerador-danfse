package dps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// declaracaoExemplo é uma nota de honorários de um escritório de advocacia
// do Simples Nacional em Aparecida de Goiânia (IBGE 5201405). CNPJs fictícios.
func declaracaoExemplo() Declaracao {
	fuso := time.FixedZone("BRT", -3*60*60)
	return Declaracao{
		Ambiente:          Homologacao,
		Emissao:           time.Date(2026, 11, 21, 13, 30, 15, 500, fuso),
		Competencia:       time.Date(2026, 11, 21, 0, 0, 0, 0, fuso),
		VersaoAplicativo:  "nfse-emissor-0.1",
		Serie:             "1",
		Numero:            42,
		CodigoMunicipioEm: "5201405",
		Prestador: Prestador{
			CNPJ:                  "11222333000181",
			OpcaoSimples:          OptanteMEEPP,
			RegimeApuracaoSimples: 1,
			RegimeEspecial:        0,
		},
		Tomador: &Tomador{
			CPF:   "52998224725",
			Nome:  "Maria  da Silva\n",
			Email: "maria@example.com",
			Endereco: &Endereco{
				CodigoMunicipio: "5208707",
				CEP:             "74000000",
				Logradouro:      "Rua 1",
				Numero:          "100",
				Bairro:          "Centro",
			},
		},
		Servico: Servico{
			CodigoMunicipioPrestacao: "5201405",
			CodigoTributacaoNacional: "171401",
			Descricao:                "Honorários advocatícios & consultoria - novembro/2026",
		},
		ValorServ: 1500,
		Tributacao: Tributacao{
			TributacaoISSQN: 1,
			RetencaoISSQN:   1,
		},
	}
}

func TestID(t *testing.T) {
	d := declaracaoExemplo()
	id, err := d.ID()
	if err != nil {
		t.Fatal(err)
	}
	want := "DPS" + "5201405" + "2" + "11222333000181" + "00001" + "000000000000042"
	if id != want {
		t.Fatalf("ID = %s, quero %s", id, want)
	}
	if len(id) != 45 {
		t.Fatalf("ID com %d posições", len(id))
	}
}

func TestXMLConteudo(t *testing.T) {
	d := declaracaoExemplo()
	got, err := d.XML()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(got)

	for _, trecho := range []string{
		`<DPS xmlns="http://www.sped.fazenda.gov.br/nfse" versao="1.01"><infDPS Id="DPS520140521122233300018100001000000000000042">`,
		`<tpAmb>2</tpAmb><dhEmi>2026-11-21T13:30:15-03:00</dhEmi>`,
		`<serie>1</serie><nDPS>42</nDPS><dCompet>2026-11-21</dCompet><tpEmit>1</tpEmit><cLocEmi>5201405</cLocEmi>`,
		`<prest><CNPJ>11222333000181</CNPJ><regTrib><opSimpNac>3</opSimpNac><regApTribSN>1</regApTribSN><regEspTrib>0</regEspTrib></regTrib></prest>`,
		`<toma><CPF>52998224725</CPF><xNome>Maria da Silva</xNome>`,
		`<cServ><cTribNac>171401</cTribNac><xDescServ>Honorários advocatícios &amp; consultoria - novembro/2026</xDescServ></cServ>`,
		`<vServPrest><vServ>1500.00</vServ></vServPrest>`,
		`<tribMun><tribISSQN>1</tribISSQN><tpRetISSQN>1</tpRetISSQN></tribMun><totTrib><indTotTrib>0</indTotTrib></totTrib>`,
	} {
		if !strings.Contains(xml, trecho) {
			t.Errorf("XML não contém %s\n%s", trecho, xml)
		}
	}
	if strings.Contains(xml, "<?xml") || strings.ContainsAny(xml, "\n\t") {
		t.Errorf("XML deve vir sem declaração e sem quebras de linha:\n%s", xml)
	}
}

func TestXMLOpcionais(t *testing.T) {
	d := declaracaoExemplo()
	d.Tomador = nil
	d.Tributacao.AliquotaISSQN = 2
	d.Tributacao.PercentualTotalSimples = 15.5
	d.Servico.InformacoesComplem = "Contrato 12/2026"
	got, err := d.XML()
	if err != nil {
		t.Fatal(err)
	}
	xml := string(got)
	for _, trecho := range []string{
		`<pAliq>2.00</pAliq>`,
		`<totTrib><pTotTribSN>15.50</pTotTribSN></totTrib>`,
		`<infoCompl><xInfComp>Contrato 12/2026</xInfComp></infoCompl>`,
	} {
		if !strings.Contains(xml, trecho) {
			t.Errorf("XML não contém %s\n%s", trecho, xml)
		}
	}
	if strings.Contains(xml, "<toma>") || strings.Contains(xml, "indTotTrib") {
		t.Errorf("campos que deveriam ser omitidos:\n%s", xml)
	}
}

func TestValidarErros(t *testing.T) {
	casos := map[string]func(*Declaracao){
		"cnpj curto":            func(d *Declaracao) { d.Prestador.CNPJ = "123" },
		"série com letra":       func(d *Declaracao) { d.Serie = "A1" },
		"número zero":           func(d *Declaracao) { d.Numero = 0 },
		"competência futura":    func(d *Declaracao) { d.Competencia = d.Emissao.AddDate(0, 1, 0) },
		"cpf e cnpj":            func(d *Declaracao) { d.Tomador.CNPJ = "11222333000181" },
		"tomador sem documento": func(d *Declaracao) { d.Tomador.CPF = "" },
		"tomador sem nome":      func(d *Declaracao) { d.Tomador.Nome = "  " },
		"valor zero":            func(d *Declaracao) { d.ValorServ = 0 },
		"cTribNac inválido":     func(d *Declaracao) { d.Servico.CodigoTributacaoNacional = "1714" },
		"descrição vazia":       func(d *Declaracao) { d.Servico.Descricao = "" },
		"regApTribSN sem MEEPP": func(d *Declaracao) { d.Prestador.OpcaoSimples = NaoOptanteSimples },
		"ambiente inválido":     func(d *Declaracao) { d.Ambiente = 3 },
		"retenção inválida":     func(d *Declaracao) { d.Tributacao.RetencaoISSQN = 0 },
	}
	for nome, mudar := range casos {
		t.Run(nome, func(t *testing.T) {
			d := declaracaoExemplo()
			mudar(&d)
			if err := d.Validar(); err == nil {
				t.Fatal("esperava erro de validação")
			}
			if _, err := d.XML(); err == nil {
				t.Fatal("XML não deveria ser gerado")
			}
		})
	}
}

// TestXMLContraSchema valida o XML gerado contra o schema oficial
// DPS_v1.01.xsd usando o xmllint, quando ele está instalado.
func TestXMLContraSchema(t *testing.T) {
	variacoes := map[string]func(*Declaracao){
		"padrão":   func(*Declaracao) {},
		"sem toma": func(d *Declaracao) { d.Tomador = nil },
		"com opcionais": func(d *Declaracao) {
			d.Tomador.CPF, d.Tomador.CNPJ = "", "11444777000161"
			d.Tributacao.AliquotaISSQN = 5
			d.Tributacao.PercentualTotalSimples = 16.93
			d.Servico.CodigoNBS = "113011000"
			d.Servico.CodigoTributacaoMunic = "001"
			d.Servico.InformacoesComplem = "Processo 0001234-56.2026.8.09.0011"
			d.Prestador.InscricaoMunicipal = "123456"
		},
	}
	for nome, mudar := range variacoes {
		t.Run(nome, func(t *testing.T) {
			d := declaracaoExemplo()
			mudar(&d)
			xml, err := d.XML()
			if err != nil {
				t.Fatal(err)
			}
			ValidarContraSchema(t, xml)
		})
	}
}

// ValidarContraSchema roda o xmllint com o schema DPS_v1.01.xsd. É exportada
// em arquivo de teste para ser reaproveitada pelos testes deste pacote.
func ValidarContraSchema(t *testing.T, xml []byte) {
	t.Helper()
	xmllint, err := exec.LookPath("xmllint")
	if err != nil {
		t.Skip("xmllint não instalado; validação contra o schema ignorada")
	}
	dir := t.TempDir()
	arq := filepath.Join(dir, "dps.xml")
	if err := os.WriteFile(arq, xml, 0o600); err != nil {
		t.Fatal(err)
	}
	schema := copiarSchemas(t, dir)
	out, err := exec.Command(xmllint, "--nonet", "--noout", "--schema", schema, arq).CombinedOutput()
	if err != nil {
		t.Fatalf("XML inválido para o schema: %v\n%s\n%s", err, out, xml)
	}
}

// copiarSchemas copia os XSD oficiais para dir e devolve o caminho do
// DPS_v1.01.xsd. O padrão de TSSerieDPS vem escrito como "^0{0,4}\d{1,5}$",
// mas em XSD "^" e "$" são caracteres literais, então o xmllint rejeitaria
// qualquer série. A cópia remove os dois para validar com o sentido pretendido.
func copiarSchemas(t *testing.T, dir string) string {
	t.Helper()
	origem := filepath.Join("testdata", "schemas")
	arquivos, err := os.ReadDir(origem)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range arquivos {
		conteudo, err := os.ReadFile(filepath.Join(origem, a.Name()))
		if err != nil {
			t.Fatal(err)
		}
		conteudo = []byte(strings.ReplaceAll(string(conteudo), `"^0{0,4}\d{1,5}$"`, `"0{0,4}\d{1,5}"`))
		if err := os.WriteFile(filepath.Join(dir, a.Name()), conteudo, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(dir, "DPS_v1.01.xsd")
}
