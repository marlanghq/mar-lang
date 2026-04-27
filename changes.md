v 0.0.6-SNAPSHOT

- Todas as tabelas passam a ter created_at e updated_at automáticos.
- Agora o belongs_to suporta nome de domínio melhor. Exemplo: belongs_to reviewer: current_user (antes para usar o current_user você era obrigado a usar o modo mais simplificado: belongs_to current_user).
- Correções de bugs relacionados ao uso de campos de data e data e hora no Admin.
- Tipo Posix foi quebrado em dois: Date e DateTime. Internamente ainda são Posix (e no retorno do json também).
- Fixed a runtime metrics vulnerability where an attack with high-cardinality unknown paths could trigger unbounded memory growth.
- As seguintes configurações foram movidas de systema para auth:
        auth_request_code_rate_limit_per_minute
        auth_login_rate_limit_per_minute
        admin_ui_session_ttl_hours
        security_frame_policy
        security_referrer_policy
        security_content_type_nosniff
- Admin UI agora é App UI. Aos poucos Mar caminha para se tornar uma solução fullstack.
- Actions agora tem suporte à rules, inclusive usando dados de load.
- Nova interface simplificada na runtime iOS.
- Actions agora podem receber referências à entidades como parâmetros (ao invés de usar Int). Isso deixa mais explicito o que é o parâmetro e também permite criar interfaces melhores no web e iOS (selects ao invés de campos de text). Ex: 
        type alias PublishPostInput =
                { post: ref Post }
