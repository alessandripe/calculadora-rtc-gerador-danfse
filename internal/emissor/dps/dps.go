// Package dps monta o XML da Declaração de Prestação de Serviços (DPS) no
// leiaute nacional da NFS-e (schema DPS_v1.01.xsd), pronto para ser assinado
// e enviado à Sefin Nacional.
//
// O pacote cobre o caso de um prestador pessoa jurídica que emite as próprias
// notas (tpEmit = 1), com o serviço prestado no município do emissor. Campos de
// comércio exterior, obra, evento, dedução e intermediário ficam de fora de
// propósito: não se aplicam a um escritório de advocacia do Simples Nacional.
package dps

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Namespace do leiaute nacional da NFS-e.
const Namespace = "http://www.sped.fazenda.gov.br/nfse"

// VersaoLeiaute é o atributo versao da DPS para o schema DPS_v1.01.xsd.
const VersaoLeiaute = "1.01"

// Ambiente de emissão (tpAmb).
type Ambiente int

const (
	Producao    Ambiente = 1
	Homologacao Ambiente = 2 // "produção restrita"
)

// String devolve o nome do ambiente em português.
func (a Ambiente) String() string {
	switch a {
	case Producao:
		return "produção"
	case Homologacao:
		return "homologação"
	default:
		return fmt.Sprintf("ambiente(%d)", int(a))
	}
}

// Situação do prestador perante o Simples Nacional (opSimpNac).
const (
	NaoOptanteSimples = 1
	OptanteMEI        = 2
	OptanteMEEPP      = 3
)

// Prestador reúne os dados fixos do emissor da nota.
type Prestador struct {
	CNPJ               string
	InscricaoMunicipal string // opcional
	Telefone           string // opcional
	Email              string // opcional

	// Regime tributário.
	OpcaoSimples          int // opSimpNac: 1, 2 ou 3
	RegimeApuracaoSimples int // regApTribSN: 1, 2 ou 3; 0 omite (só vale para ME/EPP)
	RegimeEspecial        int // regEspTrib: 0 = nenhum
}

// Endereco nacional do tomador.
type Endereco struct {
	CodigoMunicipio string `json:"codigoMunicipio"` // IBGE, 7 dígitos
	CEP             string `json:"cep"`
	Logradouro      string `json:"logradouro"`
	Numero          string `json:"numero"`
	Complemento     string `json:"complemento,omitempty"`
	Bairro          string `json:"bairro"`
}

// Tomador é o cliente que recebe o serviço. Informe CPF ou CNPJ.
type Tomador struct {
	CPF      string
	CNPJ     string
	Nome     string
	Endereco *Endereco // opcional
	Telefone string    // opcional
	Email    string    // opcional
}

// Servico descreve o serviço prestado.
type Servico struct {
	CodigoMunicipioPrestacao string // IBGE, 7 dígitos
	CodigoTributacaoNacional string // cTribNac, 6 dígitos (ex.: 171401 = advocacia)
	CodigoTributacaoMunic    string // cTribMun, 3 dígitos, opcional
	Descricao                string // xDescServ
	CodigoNBS                string // cNBS, 9 dígitos, opcional
	InformacoesComplem       string // xInfComp, opcional
}

// Tributacao municipal e total aproximado de tributos.
type Tributacao struct {
	TributacaoISSQN int // tribISSQN: 1 = operação tributável
	RetencaoISSQN   int // tpRetISSQN: 1 = não retido, 2 = retido pelo tomador
	// AliquotaISSQN em percentual (ex.: 2.5). Zero omite pAliq, que é o
	// esperado para ME/EPP que recolhe o ISS dentro do Simples.
	AliquotaISSQN float64
	// PercentualTotalSimples preenche pTotTribSN (percentual aproximado dos
	// tributos do Simples). Zero informa indTotTrib = 0 ("não informar").
	PercentualTotalSimples float64
}

// Declaracao é a entrada completa para montar uma DPS.
type Declaracao struct {
	Ambiente          Ambiente
	Emissao           time.Time // dhEmi; o fuso é preservado
	Competencia       time.Time // dCompet; só a data é usada
	VersaoAplicativo  string    // verAplic
	Serie             string    // até 5 dígitos
	Numero            int64     // nDPS, sequencial por série
	CodigoMunicipioEm string    // cLocEmi, IBGE do emissor

	Prestador  Prestador
	Tomador    *Tomador
	Servico    Servico
	ValorServ  float64 // vServ
	Tributacao Tributacao
}

var (
	soDigitos = regexp.MustCompile(`^[0-9]+$`)
	reSerie   = regexp.MustCompile(`^0{0,4}[0-9]{1,5}$`)
)

// ID monta o identificador de 45 posições da DPS:
// "DPS" + município (7) + tipo de inscrição (1) + inscrição (14) + série (5) + número (15).
// O tipo de inscrição é 2 para CNPJ.
func (d *Declaracao) ID() (string, error) {
	if err := validarDigitos("código do município de emissão", d.CodigoMunicipioEm, 7); err != nil {
		return "", err
	}
	if err := validarDigitos("CNPJ do prestador", d.Prestador.CNPJ, 14); err != nil {
		return "", err
	}
	serie, err := serieNumerica(d.Serie)
	if err != nil {
		return "", err
	}
	if d.Numero < 1 || d.Numero > 999999999999999 {
		return "", fmt.Errorf("número da DPS fora do intervalo 1 a 999999999999999: %d", d.Numero)
	}
	return fmt.Sprintf("DPS%s2%s%05d%015d", d.CodigoMunicipioEm, d.Prestador.CNPJ, serie, d.Numero), nil
}

// Validar confere os campos obrigatórios e os formatos do schema antes de montar o XML.
func (d *Declaracao) Validar() error {
	var erros []error
	add := func(err error) {
		if err != nil {
			erros = append(erros, err)
		}
	}

	if d.Ambiente != Producao && d.Ambiente != Homologacao {
		add(fmt.Errorf("ambiente inválido: %d", d.Ambiente))
	}
	if d.Emissao.IsZero() {
		add(errors.New("data de emissão não informada"))
	}
	if d.Competencia.IsZero() {
		add(errors.New("data de competência não informada"))
	} else if !d.Emissao.IsZero() && dataDe(d.Competencia).After(dataDe(d.Emissao)) {
		add(errors.New("a competência não pode ser posterior à data de emissão"))
	}
	if n := len(d.VersaoAplicativo); n < 1 || n > 20 {
		add(errors.New("versão do aplicativo deve ter de 1 a 20 caracteres"))
	}
	if _, err := d.ID(); err != nil {
		add(err)
	}

	p := d.Prestador
	if p.OpcaoSimples < 1 || p.OpcaoSimples > 3 {
		add(fmt.Errorf("opção do Simples Nacional inválida: %d", p.OpcaoSimples))
	}
	if p.RegimeApuracaoSimples != 0 {
		if p.OpcaoSimples != OptanteMEEPP {
			add(errors.New("regime de apuração do Simples só se aplica a ME/EPP (opção 3)"))
		}
		if p.RegimeApuracaoSimples < 1 || p.RegimeApuracaoSimples > 3 {
			add(fmt.Errorf("regime de apuração do Simples inválido: %d", p.RegimeApuracaoSimples))
		}
	}
	switch p.RegimeEspecial {
	case 0, 1, 2, 3, 4, 5, 6, 9:
	default:
		add(fmt.Errorf("regime especial de tributação inválido: %d", p.RegimeEspecial))
	}

	if t := d.Tomador; t != nil {
		switch {
		case t.CPF != "" && t.CNPJ != "":
			add(errors.New("informe CPF ou CNPJ do tomador, não os dois"))
		case t.CPF != "":
			add(validarDigitos("CPF do tomador", t.CPF, 11))
		case t.CNPJ != "":
			add(validarDigitos("CNPJ do tomador", t.CNPJ, 14))
		default:
			add(errors.New("informe o CPF ou o CNPJ do tomador"))
		}
		if strings.TrimSpace(t.Nome) == "" {
			add(errors.New("nome do tomador não informado"))
		}
		if e := t.Endereco; e != nil {
			add(validarDigitos("município do tomador", e.CodigoMunicipio, 7))
			add(validarDigitos("CEP do tomador", e.CEP, 8))
			if e.Logradouro == "" || e.Numero == "" || e.Bairro == "" {
				add(errors.New("endereço do tomador precisa de logradouro, número e bairro"))
			}
		}
	}

	s := d.Servico
	add(validarDigitos("município da prestação", s.CodigoMunicipioPrestacao, 7))
	add(validarDigitos("código de tributação nacional", s.CodigoTributacaoNacional, 6))
	if s.CodigoTributacaoMunic != "" {
		add(validarDigitos("código de tributação municipal", s.CodigoTributacaoMunic, 3))
	}
	if s.CodigoNBS != "" {
		add(validarDigitos("código NBS", s.CodigoNBS, 9))
	}
	if n := len([]rune(strings.TrimSpace(s.Descricao))); n < 1 || n > 2000 {
		add(errors.New("descrição do serviço deve ter de 1 a 2000 caracteres"))
	}
	if n := len([]rune(s.InformacoesComplem)); n > 2000 {
		add(errors.New("informações complementares passam de 2000 caracteres"))
	}

	if d.ValorServ <= 0 {
		add(errors.New("valor do serviço deve ser maior que zero"))
	}

	tr := d.Tributacao
	if tr.TributacaoISSQN < 1 || tr.TributacaoISSQN > 4 {
		add(fmt.Errorf("tributação do ISSQN inválida: %d", tr.TributacaoISSQN))
	}
	if tr.RetencaoISSQN < 1 || tr.RetencaoISSQN > 3 {
		add(fmt.Errorf("tipo de retenção do ISSQN inválido: %d", tr.RetencaoISSQN))
	}
	if tr.AliquotaISSQN < 0 || tr.AliquotaISSQN >= 10 {
		add(fmt.Errorf("alíquota do ISSQN fora do intervalo: %.2f", tr.AliquotaISSQN))
	}
	if tr.PercentualTotalSimples < 0 || tr.PercentualTotalSimples >= 100 {
		add(fmt.Errorf("percentual total de tributos do Simples fora do intervalo: %.2f", tr.PercentualTotalSimples))
	}

	return errors.Join(erros...)
}

// XML valida a declaração e devolve o XML da DPS sem assinatura, sem
// declaração <?xml?> e sem espaços entre elementos.
func (d *Declaracao) XML() ([]byte, error) {
	if err := d.Validar(); err != nil {
		return nil, err
	}
	id, _ := d.ID()
	serie, _ := serieNumerica(d.Serie)

	doc := xmlDPS{
		Xmlns:  Namespace,
		Versao: VersaoLeiaute,
		Inf: xmlInfDPS{
			ID:       id,
			TpAmb:    int(d.Ambiente),
			DhEmi:    d.Emissao.Truncate(time.Second).Format("2006-01-02T15:04:05-07:00"),
			VerAplic: d.VersaoAplicativo,
			Serie:    strconv.Itoa(serie),
			NDPS:     strconv.FormatInt(d.Numero, 10),
			DCompet:  d.Competencia.Format("2006-01-02"),
			TpEmit:   1,
			CLocEmi:  d.CodigoMunicipioEm,
			Prest: xmlPrest{
				CNPJ:  d.Prestador.CNPJ,
				IM:    d.Prestador.InscricaoMunicipal,
				Fone:  d.Prestador.Telefone,
				Email: d.Prestador.Email,
				RegTrib: xmlRegTrib{
					OpSimpNac:   d.Prestador.OpcaoSimples,
					RegApTribSN: optInt(d.Prestador.RegimeApuracaoSimples),
					RegEspTrib:  d.Prestador.RegimeEspecial,
				},
			},
			Serv: xmlServ{
				LocPrest: xmlLocPrest{CLocPrestacao: d.Servico.CodigoMunicipioPrestacao},
				CServ: xmlCServ{
					CTribNac:  d.Servico.CodigoTributacaoNacional,
					CTribMun:  d.Servico.CodigoTributacaoMunic,
					XDescServ: limparTexto(d.Servico.Descricao),
					CNBS:      d.Servico.CodigoNBS,
				},
			},
			Valores: xmlValores{
				VServPrest: xmlVServPrest{VServ: decimal2(d.ValorServ)},
				Trib: xmlTrib{
					TribMun: xmlTribMun{
						TribISSQN:  d.Tributacao.TributacaoISSQN,
						TpRetISSQN: d.Tributacao.RetencaoISSQN,
					},
				},
			},
		},
	}

	if d.Tributacao.AliquotaISSQN > 0 {
		doc.Inf.Valores.Trib.TribMun.PAliq = decimal2(d.Tributacao.AliquotaISSQN)
	}
	if d.Tributacao.PercentualTotalSimples > 0 {
		doc.Inf.Valores.Trib.TotTrib.PTotTribSN = decimal2(d.Tributacao.PercentualTotalSimples)
	} else {
		zero := 0
		doc.Inf.Valores.Trib.TotTrib.IndTotTrib = &zero
	}
	if info := strings.TrimSpace(d.Servico.InformacoesComplem); info != "" {
		doc.Inf.Serv.InfoCompl = &xmlInfoCompl{XInfComp: limparTexto(info)}
	}

	if t := d.Tomador; t != nil {
		toma := &xmlToma{
			CNPJ:  t.CNPJ,
			CPF:   t.CPF,
			XNome: limparTexto(t.Nome),
			Fone:  t.Telefone,
			Email: t.Email,
		}
		if e := t.Endereco; e != nil {
			toma.End = &xmlEnd{
				EndNac:  xmlEndNac{CMun: e.CodigoMunicipio, CEP: e.CEP},
				XLgr:    limparTexto(e.Logradouro),
				Nro:     limparTexto(e.Numero),
				XCpl:    limparTexto(e.Complemento),
				XBairro: limparTexto(e.Bairro),
			}
		}
		doc.Inf.Toma = toma
	}

	var buf bytes.Buffer
	if err := xml.NewEncoder(&buf).Encode(doc); err != nil {
		return nil, fmt.Errorf("gerar XML da DPS: %w", err)
	}
	return buf.Bytes(), nil
}

func validarDigitos(campo, valor string, tamanho int) error {
	if len(valor) != tamanho || !soDigitos.MatchString(valor) {
		return fmt.Errorf("%s deve ter %d dígitos numéricos: %q", campo, tamanho, valor)
	}
	return nil
}

func serieNumerica(s string) (int, error) {
	if !reSerie.MatchString(s) {
		return 0, fmt.Errorf("série da DPS deve ter até 5 dígitos: %q", s)
	}
	n, _ := strconv.Atoi(s)
	if n < 1 {
		return 0, fmt.Errorf("série da DPS deve ser maior que zero: %q", s)
	}
	return n, nil
}

func dataDe(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func decimal2(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

func optInt(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}

// limparTexto remove quebras de linha, tabulações e espaços repetidos. O
// leiaute rejeita texto com espaços no início ou no fim, e a assinatura fica
// mais previsível sem caracteres de controle.
func limparTexto(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
