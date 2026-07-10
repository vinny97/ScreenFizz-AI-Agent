Integrate the AI analysis into the existing screenfizz sync pipeline.

After parsing the website, analyse each business and store these fields in screenfizz_prospects:

- business_summary
- business_type
- recommended_use_case
- personalisation_line

The AI should return structured JSON.

Example personalisation_line:

"I noticed you regularly promote seasonal offers on your website. A digital display near your entrance could automatically showcase those promotions."

Do not generate the full email yet.

Only save these fields to the database.