# Bucket-Based ECMP
We have modified and recompiled OVS and OVN to implement Bucket-Based ECMP.

Some modifications are as follows:

In OVN: lib/actions.c
```c
static void
encode_SELECT(const struct ovnact_select *select,
             const struct ovnact_encode_params *ep,
             struct ofpbuf *ofpacts)
{
    ovs_assert(select->n_dsts >= 1);
    uint8_t resubmit_table = select->ltable + first_ptable(ep, ep->pipeline);
    uint32_t table_id = 0;
    struct ofpact_group *og;

    struct ds ds = DS_EMPTY_INITIALIZER;
    ds_put_format(&ds, "type=select,selection_method=bb-hash"); //use Bucket-Based Hash

    struct mf_subfield sf = expr_resolve_field(&select->res_field);
// ...
}
```

In OVS: ofproto/ofproto-dpif.c

```c
enum group_selection_method {
    SEL_METHOD_DEFAULT,
    SEL_METHOD_DP_HASH,
    SEL_METHOD_HASH,
    SEL_METHOD_BB_HASH  //Bucket-Based Hash
};
```

In OVS: ofproto/ofproto-dpif-xlate.c:
```c
static struct ofputil_bucket *
pick_select_group(struct xlate_ctx *ctx, struct group_dpif *group)
{

    if (ctx->was_mpls) {
        ctx_trigger_freeze(ctx);
        return NULL;
    }

    switch (group->selection_method) {
    case SEL_METHOD_DEFAULT:
        return pick_default_select_group(ctx, group);
        break;
    case SEL_METHOD_HASH:
        return pick_hash_fields_select_group(ctx, group);
        break;
    case SEL_METHOD_DP_HASH:
        return pick_dp_hash_select_group(ctx, group);
        break;
    case SEL_METHOD_BB_HASH:
        return pick_bb_hash_select_group(ctx,group);
    default:
        OVS_NOT_REACHED();
    }

    return NULL;
}
```
