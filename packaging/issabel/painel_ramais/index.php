<?php
// Modulo "Painel de Ramais" do Issabel 5 (PHP 7.4). Este arquivo so DEFINE a funcao abaixo: nenhum codigo com efeito
// fora dela (o index.php do modulo e alcancavel direto pela URL, sem ACL). Contrato: docs/plans/2026-10-16-fase2-menu-issabel.md
// secao 8b e 3 (C1). O token e assinado (HMAC-SHA256) com a chave /etc/painel-ramais-issabel.key e vale 60 s.
function _moduleContent(&$smarty, $module_name)
{
    global $arrConf, $pACL;  // $pACL vem do index.php do Issabel
    $raw = @file_get_contents('/etc/painel-ramais-issabel.key');
    $key = ($raw === false) ? false : base64_decode(trim($raw), true);
    if ($key === false || strlen($key) < 32) {
        return '<p>Painel de Ramais: integração com o Issabel desligada (chave ausente ou ilegível). '
             . 'Avise o administrador do servidor (./instalar.py status).</p>';
    }
    $user = isset($_SESSION['issabel_user']) ? (string)$_SESSION['issabel_user'] : '';
    if (!preg_match('/^[^\p{Cc}\p{Cf}\p{Zl}\p{Zp}]{1,64}\z/u', $user)) {
        return '<p>Painel de Ramais: usuário do Issabel inválido para o painel.</p>';
    }
    if (strlen('issabel:admin:' . $user) > 128) {
        return '<p>Painel de Ramais: nome de usuário do Issabel longo demais para o painel.</p>';
    }
    if (!isset($pACL)) {
        $pACL = new paloACL(new paloDB($arrConf['issabel_dsn']['acl']));
    }
    // isUserAdministratorGroup devolve o nome do grupo, false ou NULL: usar so como booleano.
    $papel = (bool)$pACL->isUserAdministratorGroup($user) ? 'admin' : 'user';
    $b64 = function ($s) { return rtrim(strtr(base64_encode($s), '+/', '-_'), '='); };
    $p = $b64(json_encode(array(
        'sub' => 'issabel:' . $papel . ':' . $user,
        'exp' => time() + 60,
        'jti' => $b64(random_bytes(16)),
    ), JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE));
    $t = $p . '.' . $b64(hash_hmac('sha256', $p, $key, true));
    return '<iframe src="/painel-ramais/#t=' . htmlspecialchars($t, ENT_QUOTES) . '" allow="fullscreen" '
         . 'title="Painel de Ramais" style="width:100%;height:calc(100vh - 140px);min-height:560px;border:0"></iframe>';
}
