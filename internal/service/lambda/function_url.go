package lambda

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/service/lambda"
	"github.com/hashicorp/aws-sdk-go-base/v2/awsv1shim/v2/tfawserr"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/flex"
)

func ResourceFunctionUrl() *schema.Resource {
	return &schema.Resource{
		CreateWithoutTimeout: resourceFunctionURLCreate,
		ReadWithoutTimeout:   resourceFunctionURLRead,
		UpdateWithoutTimeout: resourceFunctionURLUpdate,
		DeleteWithoutTimeout: resourceFunctionURLDelete,

		Importer: &schema.ResourceImporter{
			State: resourceFunctionUrlImport,
		},

		Timeouts: &schema.ResourceTimeout{
			Create: schema.DefaultTimeout(10 * time.Minute),
		},

		Schema: map[string]*schema.Schema{
			"authorization_type": {
				Type:         schema.TypeString,
				Required:     true,
				ValidateFunc: validation.StringInSlice(lambda.FunctionUrlAuthType_Values(), false),
			},
			"cors": {
				Type:     schema.TypeList,
				Optional: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"allow_credentials": {
							Type:     schema.TypeBool,
							Optional: true,
						},
						"allow_headers": {
							Type:     schema.TypeSet,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"allow_methods": {
							Type:     schema.TypeSet,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"allow_origins": {
							Type:     schema.TypeSet,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"expose_headers": {
							Type:     schema.TypeSet,
							Optional: true,
							Elem:     &schema.Schema{Type: schema.TypeString},
						},
						"max_age": {
							Type:         schema.TypeInt,
							Optional:     true,
							ValidateFunc: validation.IntAtMost(86400),
						},
					},
				},
			},
			"function_arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"function_name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
				DiffSuppressFunc: func(k, old, new string, d *schema.ResourceData) bool {
					// Using function name or ARN should not be shown as a diff.
					// Try to convert the old and new values from ARN to function name
					oldFunctionName, oldFunctionNameErr := GetFunctionNameFromARN(old)
					newFunctionName, newFunctionNameErr := GetFunctionNameFromARN(new)
					return (oldFunctionName == new && oldFunctionNameErr == nil) || (newFunctionName == old && newFunctionNameErr == nil)
				},
			},
			"function_url": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"qualifier": {
				Type:     schema.TypeString,
				ForceNew: true,
				Optional: true,
			},
		},
	}
}

func resourceFunctionURLCreate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).LambdaConn

	name := d.Get("function_name").(string)
	input := &lambda.CreateFunctionUrlConfigInput{
		AuthType:     aws.String(d.Get("authorization_type").(string)),
		FunctionName: aws.String(name),
	}

	if v, ok := d.GetOk("cors"); ok && len(v.([]interface{})) > 0 {
		input.Cors = expandFunctionUrlCorsConfigs(v.([]interface{}))
	}

	if v, ok := d.GetOk("qualifier"); ok {
		input.Qualifier = aws.String(v.(string))
	}

	log.Printf("[DEBUG] Creating Lambda Function URL: %s", input)
	output, err := conn.CreateFunctionUrlConfigWithContext(ctx, input)

	if err != nil {
		return diag.Errorf("error creating Lambda Function URL (%s): %s", name, err)
	}

	d.SetId(aws.StringValue(output.FunctionArn))

	if v := d.Get("authorization_type").(string); v == lambda.FunctionUrlAuthTypeNone {
		input := &lambda.AddPermissionInput{
			Action:              aws.String("lambda:InvokeFunctionUrl"),
			FunctionName:        aws.String(d.Get("function_name").(string)),
			FunctionUrlAuthType: aws.String(v),
			Principal:           aws.String("*"),
			StatementId:         aws.String("FunctionURLAllowPublicAccess"),
		}

		log.Printf("[DEBUG] Adding Lambda Permission: %s", input)
		_, err := conn.AddPermissionWithContext(ctx, input)

		if err != nil {
			return diag.Errorf("error adding Lambda Function URL (%s) permission %s", d.Id(), err)
		}
	}

	return resourceFunctionURLRead(ctx, d, meta)
}

func resourceFunctionURLRead(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).LambdaConn

	input := &lambda.GetFunctionUrlConfigInput{
		FunctionName: aws.String(d.Get("function_name").(string)),
	}

	if v, ok := d.GetOk("qualifier"); ok {
		input.Qualifier = aws.String(v.(string))
	}

	output, err := conn.GetFunctionUrlConfig(input)
	log.Printf("[DEBUG] Getting Lambda Function Url Config Output: %s", output)

	if err != nil {
		if awsErr, ok := err.(awserr.Error); ok && awsErr.Code() == lambda.ErrCodeResourceNotFoundException && !d.IsNewResource() {
			d.SetId("")
			return nil
		}
		return diag.Errorf("error getting Lambda Function Url Config (%s): %w", d.Id(), err)
	}

	d.Set("authorization_type", output.AuthType)
	d.Set("cors", flattenFunctionUrlCorsConfigs(output.Cors))
	d.Set("function_arn", output.FunctionArn)
	d.Set("function_url", output.FunctionUrl)

	return nil
}

func resourceFunctionURLUpdate(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).LambdaConn

	log.Printf("[DEBUG] Updating Lambda Function Url: %s", d.Id())

	params := &lambda.UpdateFunctionUrlConfigInput{
		FunctionName: aws.String(d.Get("function_name").(string)),
	}

	if v, ok := d.GetOk("qualifier"); ok {
		params.Qualifier = aws.String(v.(string))
	}

	if d.HasChange("authorization_type") {
		params.AuthType = aws.String(d.Get("authorization_type").(string))
	}

	if d.HasChange("cors") {
		params.Cors = expandFunctionUrlCorsConfigs(d.Get("cors").([]interface{}))
	}

	_, err := conn.UpdateFunctionUrlConfig(params)

	if err != nil {
		return diag.Errorf("error updating Lambda Function Url (%s): %s", d.Id(), err)
	}

	return resourceFunctionURLRead(ctx, d, meta)
}

func resourceFunctionURLDelete(ctx context.Context, d *schema.ResourceData, meta interface{}) diag.Diagnostics {
	conn := meta.(*conns.AWSClient).LambdaConn

	log.Printf("[INFO] Deleting Lambda Function Url: %s", d.Id())

	params := &lambda.DeleteFunctionUrlConfigInput{
		FunctionName: aws.String(d.Get("function_name").(string)),
	}

	if v, ok := d.GetOk("qualifier"); ok {
		params.Qualifier = aws.String(v.(string))
	}

	_, err := conn.DeleteFunctionUrlConfig(params)

	if tfawserr.ErrCodeEquals(err, lambda.ErrCodeResourceNotFoundException) {
		return nil
	}

	if err != nil {
		return diag.Errorf("error deleting Lambda Function Url (%s): %s", d.Id(), err)
	}

	return nil
}

func expandFunctionUrlCorsConfigs(urlConfigMap []interface{}) *lambda.Cors {
	cors := &lambda.Cors{}
	if len(urlConfigMap) == 1 && urlConfigMap[0] != nil {
		config := urlConfigMap[0].(map[string]interface{})
		cors.AllowCredentials = aws.Bool(config["allow_credentials"].(bool))
		if len(config["allow_headers"].([]interface{})) > 0 {
			cors.AllowHeaders = flex.ExpandStringList(config["allow_headers"].([]interface{}))
		}
		if len(config["allow_methods"].([]interface{})) > 0 {
			cors.AllowMethods = flex.ExpandStringList(config["allow_methods"].([]interface{}))
		}
		if len(config["allow_origins"].([]interface{})) > 0 {
			cors.AllowOrigins = flex.ExpandStringList(config["allow_origins"].([]interface{}))
		}
		if len(config["expose_headers"].([]interface{})) > 0 {
			cors.ExposeHeaders = flex.ExpandStringList(config["expose_headers"].([]interface{}))
		}
		if config["max_age"].(int) > 0 {
			cors.MaxAge = aws.Int64(int64(config["max_age"].(int)))
		}
	}
	return cors
}

func flattenFunctionUrlCorsConfigs(cors *lambda.Cors) []map[string]interface{} {
	settings := make(map[string]interface{})

	if cors == nil {
		return nil
	}

	settings["allow_credentials"] = cors.AllowCredentials
	settings["allow_headers"] = cors.AllowHeaders
	settings["allow_methods"] = cors.AllowMethods
	settings["allow_origins"] = cors.AllowOrigins
	settings["expose_headers"] = cors.ExposeHeaders
	settings["max_age"] = cors.MaxAge

	return []map[string]interface{}{settings}
}

func resourceFunctionUrlImport(d *schema.ResourceData, meta interface{}) ([]*schema.ResourceData, error) {

	idSplit := strings.Split(d.Id(), ":")

	functionName := idSplit[len(idSplit)-2]
	qualifier := idSplit[len(idSplit)-1]

	d.Set("function_name", functionName)
	d.Set("qualifier", qualifier)

	return []*schema.ResourceData{d}, nil
}
