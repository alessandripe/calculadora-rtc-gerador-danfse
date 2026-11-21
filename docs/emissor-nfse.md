# Emissor de NFS-e (nfse-emissor)

Ferramenta de linha de comando que emite NFS-e no Sistema Nacional (Sefin
Nacional) para um escritório de advocacia optante pelo Simples Nacional. Ela
monta a DPS, assina com o certificado A1 do escritório e envia pela API, com
autenticação mútua (mTLS) usando o mesmo certificado.

Esta é a etapa 1: emissão e consulta, testadas primeiro no ambiente de
homologação (produção restrita, sem valor fiscal). Ainda não estão aqui o PDF
(DANFSe), a emissão recorrente e o cancelamento.

## Preparar

1. Copie `cmd/nfse-emissor/exemplo/escritorio.json` para uma pasta sua e
   preencha o CNPJ e o caminho do certificado `.pfx`. Os demais campos já vêm
   com os valores de um escritório de advocacia ME/EPP do Simples em
   Aparecida de Goiânia (IBGE 5201405, código de serviço 171401).
2. Crie um `nota.json` por nota (veja o exemplo).
3. Defina a senha do certificado na variável de ambiente, nunca no arquivo:

   ```bash
   export NFSE_CERT_SENHA='senha-do-certificado'
   ```

## Usar

```bash
# Só monta e assina; grava o XML em notas/homologacao/ para conferência
go run ./cmd/nfse-emissor emitir -config ~/nfse/escritorio.json -nota ~/nfse/nota.json

# Envia para a homologação
go run ./cmd/nfse-emissor emitir -config ~/nfse/escritorio.json -nota ~/nfse/nota.json -enviar

# Consulta uma nota pela chave de acesso
go run ./cmd/nfse-emissor consultar -config ~/nfse/escritorio.json -chave <chave>

# Descobre se uma DPS já virou nota (útil se a conexão cair no envio)
go run ./cmd/nfse-emissor dps -config ~/nfse/escritorio.json -id DPS...
```

Para produção, acrescente `-ambiente producao`. O padrão é sempre homologação.

A numeração das DPS fica em `numeracao.json`, na mesma pasta do
`escritorio.json`, separada por ambiente e série. O número é reservado antes
do envio, então uma falha deixa um número pulado, mas nunca repete número.

## Campos da nota

| Campo | Obrigatório | Observação |
|---|---|---|
| `tomador.cpf` ou `tomador.cnpj` | sim | pode ter pontuação |
| `tomador.nome` | sim | |
| `tomador.email`, `tomador.telefone`, `tomador.endereco` | não | |
| `descricao` | sim | até 2000 caracteres |
| `valor` | sim | em reais |
| `competencia` | não | `AAAA-MM-DD`; padrão é a data de emissão |
| `issRetido` | não | `true` quando o tomador retém o ISS |
| `informacoesComplementares` | não | |

## Decisões técnicas

- **Leiaute:** schema `DPS_v1.01.xsd` (cópia em
  `internal/emissor/dps/testdata/schemas`). Os testes validam todo XML gerado,
  inclusive já assinado, contra esse schema com `xmllint`.
- **Assinatura:** XMLDSig com C14N 1.0 inclusiva, transformações
  enveloped-signature + C14N, `Reference` para o `Id` de `infDPS`, RSA-SHA1, e
  `Signature` sem prefixo logo após `infDPS`. É o formato usado pelas
  bibliotecas que já emitem em produção. Se a Sefin passar a exigir SHA-256,
  basta trocar `assinatura.SHA1` por `assinatura.SHA256`.
- **Prestador:** só CNPJ, inscrição municipal e regime vão na DPS. Nome e
  endereço o Sistema Nacional puxa do cadastro.
- **Simples Nacional:** `opSimpNac=3` (ME/EPP), `regApTribSN=1` e
  `regEspTrib=0`. Por padrão vai `indTotTrib=0` (não informar o total
  aproximado de tributos) e não vai `pAliq`; os dois podem ser configurados.
- **dhEmi:** horário de Brasília recuado 1 minuto, porque a Sefin rejeita
  emissão "no futuro" quando os relógios diferem um pouco.
- **IBS/CBS:** o grupo `IBSCBS` ainda não é enviado. Ele é opcional no schema
  atual; a integração com a calculadora do Simples entra na etapa 2.

## A conferir na primeira emissão em homologação

- Se Aparecida de Goiânia exige o código de tributação municipal (`cTribMun`)
  ou o código NBS para advocacia. Se a Sefin rejeitar por isso, basta
  preencher no `escritorio.json`.
- Se a série `1` é aceita para emissão via API (erro E0010 indica série fora
  da faixa permitida).
