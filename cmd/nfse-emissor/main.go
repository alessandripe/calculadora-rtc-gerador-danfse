// Comando nfse-emissor emite NFS-e no Sistema Nacional para o escritório.
//
// Uso:
//
//	nfse-emissor emitir    -config escritorio.json -nota nota.json [-enviar] [-ambiente homologacao|producao]
//	nfse-emissor consultar -config escritorio.json -chave CHAVE   [-ambiente ...]
//	nfse-emissor dps       -config escritorio.json -id DPS...     [-ambiente ...]
//
// Sem -enviar, o emitir só monta e assina a DPS e grava o XML em disco, para
// conferência. A senha do certificado vem da variável NFSE_CERT_SENHA.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"danfse-service/internal/emissor"
	"danfse-service/internal/emissor/assinatura"
	"danfse-service/internal/emissor/dps"
	"danfse-service/internal/emissor/sefin"

	"golang.org/x/term"
)

func main() {
	if err := executar(context.Background(), os.Args[1:], os.Stdout, time.Now); err != nil {
		fmt.Fprintln(os.Stderr, "erro:", err)
		os.Exit(1)
	}
}

// urlsSefin pode ser trocado nos testes para apontar a um servidor local.
var urlsSefin = map[dps.Ambiente]string{
	dps.Homologacao: sefin.URLHomologacao,
	dps.Producao:    sefin.URLProducao,
}

// novoCliente pode ser trocado nos testes.
var novoCliente = func(amb dps.Ambiente, cert *assinatura.Certificado) *sefin.Cliente {
	return sefin.Novo(urlsSefin[amb], cert.TLS())
}

const ajuda = `uso: nfse-emissor <comando> [opções]

comandos:
  emitir     monta, assina e (com -enviar) envia uma nota
  consultar  baixa o XML de uma NFS-e pela chave de acesso
  dps        descobre a chave da NFS-e gerada por uma DPS

Use "nfse-emissor <comando> -h" para ver as opções.`

func executar(ctx context.Context, args []string, out io.Writer, agora func() time.Time) error {
	if len(args) == 0 {
		return errors.New(ajuda)
	}
	cmd, args := args[0], args[1:]

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(out)
	config := fs.String("config", "escritorio.json", "arquivo com os dados fixos do escritório")
	ambiente := fs.String("ambiente", "homologacao", "homologacao (testes, sem valor fiscal) ou producao")
	saida := fs.String("saida", "notas", "pasta onde os XML são gravados")
	var nota, chave, id *string
	var enviar *bool
	switch cmd {
	case "emitir":
		nota = fs.String("nota", "nota.json", "arquivo com os dados da nota")
		enviar = fs.Bool("enviar", false, "envia a DPS para a Sefin (sem isso, só gera o XML assinado)")
	case "consultar":
		chave = fs.String("chave", "", "chave de acesso da NFS-e (50 posições)")
	case "dps":
		id = fs.String("id", "", "identificador da DPS (45 posições, começa com DPS)")
	case "-h", "--help", "ajuda":
		fmt.Fprintln(out, ajuda)
		return nil
	default:
		return fmt.Errorf("comando desconhecido %q\n\n%s", cmd, ajuda)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	amb, err := lerAmbiente(*ambiente)
	if err != nil {
		return err
	}
	var esc emissor.Escritorio
	if err := emissor.LerJSON(*config, &esc); err != nil {
		return err
	}
	esc.AplicarPadroes()
	baseConfig := filepath.Dir(*config)

	switch cmd {
	case "emitir":
		return emitir(ctx, out, esc, baseConfig, *nota, amb, *enviar, *saida, agora())
	case "consultar":
		if *chave == "" {
			return errors.New("informe -chave")
		}
		cert, err := carregarCertificado(esc, baseConfig, agora())
		if err != nil {
			return err
		}
		n, err := novoCliente(amb, cert).ConsultarNFSe(ctx, *chave)
		if err != nil {
			return err
		}
		arq, err := gravar(*saida, amb, "nfse-"+n.ChaveAcesso+".xml", n.XML)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "NFS-e %s gravada em %s\n", n.ChaveAcesso, arq)
		return nil
	default: // dps
		if *id == "" {
			return errors.New("informe -id")
		}
		cert, err := carregarCertificado(esc, baseConfig, agora())
		if err != nil {
			return err
		}
		ch, err := novoCliente(amb, cert).ChavePorDPS(ctx, *id)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "a DPS %s gerou a NFS-e %s\n", *id, ch)
		return nil
	}
}

func emitir(ctx context.Context, out io.Writer, esc emissor.Escritorio, baseConfig, arqNota string, amb dps.Ambiente, enviar bool, saida string, agora time.Time) error {
	var n emissor.Nota
	if err := emissor.LerJSON(arqNota, &n); err != nil {
		return err
	}
	cert, err := carregarCertificado(esc, baseConfig, agora)
	if err != nil {
		return err
	}
	if cnpj := cert.CNPJ(); cnpj != "" && cnpj != esc.CNPJ {
		return fmt.Errorf("o certificado é do CNPJ %s, mas o escritório está configurado com %s", cnpj, esc.CNPJ)
	}

	// Confere os dados antes de gastar um número.
	if _, err := emissor.MontarDeclaracao(esc, n, amb, 1, agora); err != nil {
		return fmt.Errorf("dados da nota inválidos:\n%w", err)
	}

	numeracao, err := emissor.AbrirNumeracao(filepath.Join(baseConfig, "numeracao.json"))
	if err != nil {
		return err
	}
	numero, err := numeracao.Reservar(amb, esc.Serie)
	if err != nil {
		return err
	}
	decl, err := emissor.MontarDeclaracao(esc, n, amb, numero, agora)
	if err != nil {
		return err
	}
	id, _ := decl.ID()
	xml, err := decl.XML()
	if err != nil {
		return err
	}
	assinado, err := cert.AssinarDPS(xml, assinatura.SHA1)
	if err != nil {
		return err
	}
	arqDPS, err := gravar(saida, amb, id+".xml", assinado)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "DPS nº %d (série %s, %s) assinada: %s\n", numero, esc.Serie, amb, arqDPS)

	if !enviar {
		fmt.Fprintln(out, "Nada foi enviado. Confira o XML e rode de novo com -enviar para emitir.")
		return nil
	}

	nota, err := novoCliente(amb, cert).Emitir(ctx, assinado)
	if err != nil {
		if rej, ok := sefin.EhRejeicao(err); ok {
			return fmt.Errorf("%w\n(DPS %s não virou nota; corrija e emita de novo)", rej, id)
		}
		return fmt.Errorf("%w\nNão dá para saber se a nota foi gerada. Confira com: nfse-emissor dps -ambiente %s -id %s", err, nomeAmbiente(amb), id)
	}
	for _, a := range nota.Alertas {
		fmt.Fprintln(out, "alerta da Sefin:", a)
	}
	arqNFSe, err := gravar(saida, amb, "nfse-"+nota.ChaveAcesso+".xml", nota.XML)
	if err != nil {
		return fmt.Errorf("nota %s emitida, mas não consegui gravar o XML: %w", nota.ChaveAcesso, err)
	}
	fmt.Fprintf(out, "NFS-e emitida em %s. Chave de acesso: %s\nXML: %s\n", amb, nota.ChaveAcesso, arqNFSe)
	return nil
}

func carregarCertificado(esc emissor.Escritorio, baseConfig string, agora time.Time) (*assinatura.Certificado, error) {
	if esc.Certificado == "" {
		return nil, errors.New(`informe o caminho do certificado no campo "certificado" do escritorio.json`)
	}
	caminho := esc.Certificado
	if !filepath.IsAbs(caminho) {
		caminho = filepath.Join(baseConfig, caminho)
	}
	senha, ok := os.LookupEnv("NFSE_CERT_SENHA")
	if !ok {
		var err error
		if senha, err = pedirSenha(); err != nil {
			return nil, err
		}
	}
	cert, err := assinatura.CarregarPFX(caminho, senha)
	if err != nil {
		return nil, err
	}
	if err := cert.Validade(agora); err != nil {
		return nil, err
	}
	return cert, nil
}

// pedirSenha pede a senha do certificado no terminal, sem mostrar o que é
// digitado. Fora de um terminal, exige a variável NFSE_CERT_SENHA.
var pedirSenha = func() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("defina a senha do certificado na variável de ambiente NFSE_CERT_SENHA")
	}
	fmt.Fprint(os.Stderr, "Senha do certificado (não aparece ao digitar): ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fmt.Errorf("ler senha: %w", err)
	}
	return string(b), nil
}

func lerAmbiente(s string) (dps.Ambiente, error) {
	switch s {
	case "homologacao", "homologação", "2":
		return dps.Homologacao, nil
	case "producao", "produção", "1":
		return dps.Producao, nil
	}
	return 0, fmt.Errorf("ambiente deve ser homologacao ou producao, não %q", s)
}

func nomeAmbiente(a dps.Ambiente) string {
	if a == dps.Producao {
		return "producao"
	}
	return "homologacao"
}

func gravar(saida string, amb dps.Ambiente, nome string, conteudo []byte) (string, error) {
	dir := filepath.Join(saida, nomeAmbiente(amb))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	arq := filepath.Join(dir, nome)
	if err := os.WriteFile(arq, conteudo, 0o600); err != nil {
		return "", err
	}
	return arq, nil
}
