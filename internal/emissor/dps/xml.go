package dps

import "encoding/xml"

// As structs abaixo seguem a ordem exata dos elementos do schema
// tiposComplexos_v1.01.xsd. Não reordene os campos.

type xmlDPS struct {
	XMLName xml.Name  `xml:"DPS"`
	Xmlns   string    `xml:"xmlns,attr"`
	Versao  string    `xml:"versao,attr"`
	Inf     xmlInfDPS `xml:"infDPS"`
}

type xmlInfDPS struct {
	ID       string     `xml:"Id,attr"`
	TpAmb    int        `xml:"tpAmb"`
	DhEmi    string     `xml:"dhEmi"`
	VerAplic string     `xml:"verAplic"`
	Serie    string     `xml:"serie"`
	NDPS     string     `xml:"nDPS"`
	DCompet  string     `xml:"dCompet"`
	TpEmit   int        `xml:"tpEmit"`
	CLocEmi  string     `xml:"cLocEmi"`
	Prest    xmlPrest   `xml:"prest"`
	Toma     *xmlToma   `xml:"toma,omitempty"`
	Serv     xmlServ    `xml:"serv"`
	Valores  xmlValores `xml:"valores"`
}

type xmlPrest struct {
	CNPJ    string     `xml:"CNPJ"`
	IM      string     `xml:"IM,omitempty"`
	Fone    string     `xml:"fone,omitempty"`
	Email   string     `xml:"email,omitempty"`
	RegTrib xmlRegTrib `xml:"regTrib"`
}

type xmlRegTrib struct {
	OpSimpNac   int  `xml:"opSimpNac"`
	RegApTribSN *int `xml:"regApTribSN,omitempty"`
	RegEspTrib  int  `xml:"regEspTrib"`
}

type xmlToma struct {
	CNPJ  string  `xml:"CNPJ,omitempty"`
	CPF   string  `xml:"CPF,omitempty"`
	XNome string  `xml:"xNome"`
	End   *xmlEnd `xml:"end,omitempty"`
	Fone  string  `xml:"fone,omitempty"`
	Email string  `xml:"email,omitempty"`
}

type xmlEnd struct {
	EndNac  xmlEndNac `xml:"endNac"`
	XLgr    string    `xml:"xLgr"`
	Nro     string    `xml:"nro"`
	XCpl    string    `xml:"xCpl,omitempty"`
	XBairro string    `xml:"xBairro"`
}

type xmlEndNac struct {
	CMun string `xml:"cMun"`
	CEP  string `xml:"CEP"`
}

type xmlServ struct {
	LocPrest  xmlLocPrest   `xml:"locPrest"`
	CServ     xmlCServ      `xml:"cServ"`
	InfoCompl *xmlInfoCompl `xml:"infoCompl,omitempty"`
}

type xmlLocPrest struct {
	CLocPrestacao string `xml:"cLocPrestacao"`
}

type xmlCServ struct {
	CTribNac  string `xml:"cTribNac"`
	CTribMun  string `xml:"cTribMun,omitempty"`
	XDescServ string `xml:"xDescServ"`
	CNBS      string `xml:"cNBS,omitempty"`
}

type xmlInfoCompl struct {
	XInfComp string `xml:"xInfComp"`
}

type xmlValores struct {
	VServPrest xmlVServPrest `xml:"vServPrest"`
	Trib       xmlTrib       `xml:"trib"`
}

type xmlVServPrest struct {
	VServ string `xml:"vServ"`
}

type xmlTrib struct {
	TribMun xmlTribMun `xml:"tribMun"`
	TotTrib xmlTotTrib `xml:"totTrib"`
}

type xmlTribMun struct {
	TribISSQN  int    `xml:"tribISSQN"`
	TpRetISSQN int    `xml:"tpRetISSQN"`
	PAliq      string `xml:"pAliq,omitempty"`
}

type xmlTotTrib struct {
	IndTotTrib *int   `xml:"indTotTrib,omitempty"`
	PTotTribSN string `xml:"pTotTribSN,omitempty"`
}
