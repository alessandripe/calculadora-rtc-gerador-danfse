// Package emissor junta as peças da emissão de NFS-e do escritório: os dados
// fixos (configuração), os dados de cada nota, a numeração das DPS, a
// assinatura e o envio à Sefin Nacional.
package emissor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"danfse-service/internal/emissor/dps"
)

// CodigoAparecidaDeGoiania é o código IBGE de Aparecida de Goiânia-GO.
const CodigoAparecidaDeGoiania = "5201405"

// CodigoAdvocacia é o código de tributação nacional do subitem 17.14 da
// LC 116/2003 (advocacia), desdobro 01.
const CodigoAdvocacia = "171401"

// Escritorio são os dados fixos do prestador, lidos de um arquivo JSON.
// Nenhum segredo fica aqui: a senha do certificado vem da variável de
// ambiente NFSE_CERT_SENHA.
type Escritorio struct {
	CNPJ               string `json:"cnpj"`
	InscricaoMunicipal string `json:"inscricaoMunicipal,omitempty"`
	CodigoMunicipio    string `json:"codigoMunicipio"`
	Telefone           string `json:"telefone,omitempty"`
	Email              string `json:"email,omitempty"`

	Certificado string `json:"certificado"` // caminho do .pfx
	Serie       string `json:"serie"`

	Simples struct {
		Opcao          int `json:"opcao"`          // 3 = ME/EPP
		RegimeApuracao int `json:"regimeApuracao"` // 1 = tributos federais e ISS pelo Simples
		RegimeEspecial int `json:"regimeEspecial"` // 0 = nenhum
	} `json:"simples"`

	Servico struct {
		CodigoTributacaoNacional  string `json:"codigoTributacaoNacional"`
		CodigoTributacaoMunicipal string `json:"codigoTributacaoMunicipal,omitempty"`
		CodigoNBS                 string `json:"codigoNBS,omitempty"`
	} `json:"servico"`

	Tributacao struct {
		AliquotaISSQN          float64 `json:"aliquotaISSQN,omitempty"`
		PercentualTotalSimples float64 `json:"percentualTotalSimples,omitempty"`
	} `json:"tributacao"`
}

// Nota são os dados que mudam a cada emissão.
type Nota struct {
	Tomador struct {
		CPF      string        `json:"cpf,omitempty"`
		CNPJ     string        `json:"cnpj,omitempty"`
		Nome     string        `json:"nome"`
		Email    string        `json:"email,omitempty"`
		Telefone string        `json:"telefone,omitempty"`
		Endereco *dps.Endereco `json:"endereco,omitempty"`
	} `json:"tomador"`
	Descricao        string  `json:"descricao"`
	Valor            float64 `json:"valor"`
	Competencia      string  `json:"competencia,omitempty"` // AAAA-MM-DD; vazio = hoje
	ISSRetido        bool    `json:"issRetido,omitempty"`
	InformacoesCompl string  `json:"informacoesComplementares,omitempty"`
}

// LerJSON lê um arquivo JSON rejeitando campos desconhecidos, para que um
// erro de digitação no nome de um campo não passe em silêncio.
func LerJSON(caminho string, destino any) error {
	f, err := os.Open(caminho)
	if err != nil {
		return err
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(destino); err != nil {
		return fmt.Errorf("ler %s: %w", filepath.Base(caminho), err)
	}
	return nil
}

// AplicarPadroes completa a configuração com os valores de um escritório de
// advocacia ME/EPP do Simples Nacional em Aparecida de Goiânia.
func (e *Escritorio) AplicarPadroes() {
	if e.CodigoMunicipio == "" {
		e.CodigoMunicipio = CodigoAparecidaDeGoiania
	}
	if e.Serie == "" {
		e.Serie = "1"
	}
	if e.Simples.Opcao == 0 {
		e.Simples.Opcao = dps.OptanteMEEPP
	}
	if e.Simples.RegimeApuracao == 0 && e.Simples.Opcao == dps.OptanteMEEPP {
		e.Simples.RegimeApuracao = 1
	}
	if e.Servico.CodigoTributacaoNacional == "" {
		e.Servico.CodigoTributacaoNacional = CodigoAdvocacia
	}
}

// Fuso de Brasília, usado em dhEmi e na data de competência.
var Brasilia = time.FixedZone("BRT", -3*60*60)

// MontarDeclaracao combina escritório, nota e número numa DPS pronta para
// assinar. agora é o horário de emissão; ele é recuado 1 minuto porque a
// Sefin rejeita DPS com dhEmi à frente do relógio dela.
func MontarDeclaracao(e Escritorio, n Nota, amb dps.Ambiente, numero int64, agora time.Time) (*dps.Declaracao, error) {
	emissao := agora.In(Brasilia).Add(-time.Minute)
	competencia := emissao
	if c := strings.TrimSpace(n.Competencia); c != "" {
		t, err := time.ParseInLocation("2006-01-02", c, Brasilia)
		if err != nil {
			return nil, fmt.Errorf("competência deve estar no formato AAAA-MM-DD: %q", c)
		}
		competencia = t
	}
	retencao := 1
	if n.ISSRetido {
		retencao = 2
	}

	d := &dps.Declaracao{
		Ambiente:          amb,
		Emissao:           emissao,
		Competencia:       competencia,
		VersaoAplicativo:  "nfse-emissor-1.0",
		Serie:             e.Serie,
		Numero:            numero,
		CodigoMunicipioEm: e.CodigoMunicipio,
		Prestador: dps.Prestador{
			CNPJ:                  e.CNPJ,
			InscricaoMunicipal:    e.InscricaoMunicipal,
			Telefone:              e.Telefone,
			Email:                 e.Email,
			OpcaoSimples:          e.Simples.Opcao,
			RegimeApuracaoSimples: e.Simples.RegimeApuracao,
			RegimeEspecial:        e.Simples.RegimeEspecial,
		},
		Tomador: &dps.Tomador{
			CPF:      soDigitos(n.Tomador.CPF),
			CNPJ:     soDigitos(n.Tomador.CNPJ),
			Nome:     n.Tomador.Nome,
			Email:    n.Tomador.Email,
			Telefone: soDigitos(n.Tomador.Telefone),
			Endereco: n.Tomador.Endereco,
		},
		Servico: dps.Servico{
			CodigoMunicipioPrestacao: e.CodigoMunicipio,
			CodigoTributacaoNacional: e.Servico.CodigoTributacaoNacional,
			CodigoTributacaoMunic:    e.Servico.CodigoTributacaoMunicipal,
			Descricao:                n.Descricao,
			CodigoNBS:                e.Servico.CodigoNBS,
			InformacoesComplem:       n.InformacoesCompl,
		},
		ValorServ: n.Valor,
		Tributacao: dps.Tributacao{
			TributacaoISSQN:        1,
			RetencaoISSQN:          retencao,
			AliquotaISSQN:          e.Tributacao.AliquotaISSQN,
			PercentualTotalSimples: e.Tributacao.PercentualTotalSimples,
		},
	}
	if d.Tomador.Endereco != nil {
		end := *d.Tomador.Endereco
		end.CEP = soDigitos(end.CEP)
		d.Tomador.Endereco = &end
	}
	if err := d.Validar(); err != nil {
		return nil, err
	}
	return d, nil
}

func soDigitos(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Numeracao guarda o último número de DPS usado, por ambiente e série, num
// arquivo JSON. O número é reservado antes do envio: se o envio falhar, o
// número fica pulado, o que o Sistema Nacional aceita, e nunca há duas DPS
// diferentes com o mesmo número.
type Numeracao struct {
	caminho string
	Ultimos map[string]int64 `json:"ultimos"`
}

// AbrirNumeracao lê o arquivo de numeração, criando um vazio se não existir.
func AbrirNumeracao(caminho string) (*Numeracao, error) {
	n := &Numeracao{caminho: caminho, Ultimos: map[string]int64{}}
	dados, err := os.ReadFile(caminho)
	if errors.Is(err, os.ErrNotExist) {
		return n, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(dados, n); err != nil {
		return nil, fmt.Errorf("arquivo de numeração corrompido (%s): %w", caminho, err)
	}
	if n.Ultimos == nil {
		n.Ultimos = map[string]int64{}
	}
	return n, nil
}

// Reservar devolve o próximo número e grava o arquivo antes de retornar.
func (n *Numeracao) Reservar(amb dps.Ambiente, serie string) (int64, error) {
	chave := fmt.Sprintf("%d/%s", amb, serie)
	prox := n.Ultimos[chave] + 1
	n.Ultimos[chave] = prox
	dados, _ := json.MarshalIndent(n, "", "  ")
	tmp := n.caminho + ".tmp"
	if err := os.WriteFile(tmp, dados, 0o600); err != nil {
		return 0, fmt.Errorf("gravar numeração: %w", err)
	}
	if err := os.Rename(tmp, n.caminho); err != nil {
		return 0, fmt.Errorf("gravar numeração: %w", err)
	}
	return prox, nil
}
